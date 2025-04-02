package tls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ocsp"
	"google.golang.org/grpc/credentials"
)

// TLSConfig 是TLS配置的强化版
type TLSConfig struct {
	// 证书文件路径
	CertFile string
	// 私钥文件路径
	KeyFile string
	// CA证书文件路径
	CAFile string
	// 是否验证客户端证书
	ClientAuth tls.ClientAuthType
	// 服务名
	ServerName string
	// 最小TLS版本
	MinVersion uint16
	// 最大TLS版本
	MaxVersion uint16
	// 是否启用OCSP校验
	EnableOCSP bool
	// 允许的密码套件
	CipherSuites []uint16
	// 证书轮转检查间隔
	RotationInterval time.Duration
	// OCSP检查间隔
	OCSPCheckInterval time.Duration
	// 证书过期前预警时间
	ExpiryWarningPeriod time.Duration
	// 监听证书变化的channel
	certChangeChan chan struct{}
	// 互斥锁
	mu sync.RWMutex
	// 当前配置
	currentConfig *tls.Config
	// 轮转停止channel
	stopRotation chan struct{}
}

// DefaultTLSConfig 返回默认TLS配置
func DefaultTLSConfig() *TLSConfig {
	return &TLSConfig{
		ClientAuth:          tls.NoClientCert,
		MinVersion:          tls.VersionTLS12,
		MaxVersion:          tls.VersionTLS13,
		EnableOCSP:          false,
		RotationInterval:    time.Minute * 5,
		OCSPCheckInterval:   time.Hour * 6,
		ExpiryWarningPeriod: time.Hour * 24 * 7, // 7天
		certChangeChan:      make(chan struct{}, 1),
		stopRotation:        make(chan struct{}),
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
	}
}

// SetupServerConfig 设置服务端TLS配置
func (t *TLSConfig) SetupServerConfig() (*tls.Config, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	config, err := t.buildConfig()
	if err != nil {
		return nil, err
	}

	t.currentConfig = config
	t.startRotation()

	return config, nil
}

// SetupClientConfig 设置客户端TLS配置
func (t *TLSConfig) SetupClientConfig() (*tls.Config, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	config, err := t.buildConfig()
	if err != nil {
		return nil, err
	}

	if t.ServerName != "" {
		config.ServerName = t.ServerName
	}

	t.currentConfig = config
	t.startRotation()

	return config, nil
}

// ToGRPCOptions 生成gRPC TLS选项
func (t *TLSConfig) ToGRPCOptions() (credentials.TransportCredentials, error) {
	var creds credentials.TransportCredentials
	var err error

	// 区分服务端和客户端
	if t.ClientAuth == tls.NoClientCert {
		// 客户端模式
		config, err := t.SetupClientConfig()
		if err != nil {
			return nil, err
		}
		creds = credentials.NewTLS(config)
	} else {
		// 服务端模式
		config, err := t.SetupServerConfig()
		if err != nil {
			return nil, err
		}
		creds = credentials.NewTLS(config)
	}

	return creds, err
}

// buildConfig 构建TLS配置
func (t *TLSConfig) buildConfig() (*tls.Config, error) {
	// 创建基本TLS配置
	config := &tls.Config{
		MinVersion:   t.MinVersion,
		MaxVersion:   t.MaxVersion,
		CipherSuites: t.CipherSuites,
		ClientAuth:   t.ClientAuth,
	}

	// 加载证书
	if t.CertFile != "" && t.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("加载证书失败: %v", err)
		}
		config.Certificates = []tls.Certificate{cert}

		// 检查证书过期时间
		t.checkCertExpiry(cert)
	}

	// 加载CA证书
	if t.CAFile != "" {
		caCert, err := ioutil.ReadFile(t.CAFile)
		if err != nil {
			return nil, fmt.Errorf("读取CA证书失败: %v", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("解析CA证书失败")
		}

		config.RootCAs = caCertPool
		config.ClientCAs = caCertPool
	}

	// 启用OCSP校验
	if t.EnableOCSP {
		config.VerifyConnection = t.verifyConnectionWithOCSP
	}

	return config, nil
}

// reloadConfig 重新加载TLS配置
func (t *TLSConfig) reloadConfig() error {
	config, err := t.buildConfig()
	if err != nil {
		return err
	}

	t.mu.Lock()
	t.currentConfig = config
	t.mu.Unlock()

	// 通知配置已更新
	select {
	case t.certChangeChan <- struct{}{}:
	default:
	}

	return nil
}

// checkCertExpiry 检查证书过期时间并发出警告
func (t *TLSConfig) checkCertExpiry(cert tls.Certificate) {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		log.Printf("解析证书失败: %v", err)
		return
	}

	now := time.Now()
	if now.After(leaf.NotAfter) {
		log.Printf("警告: 证书已过期! 证书路径: %s", t.CertFile)
	} else if now.Add(t.ExpiryWarningPeriod).After(leaf.NotAfter) {
		remaining := leaf.NotAfter.Sub(now)
		log.Printf("警告: 证书即将过期! 剩余时间: %v, 证书路径: %s", remaining.Round(time.Hour), t.CertFile)
	}
}

// startRotation 启动证书轮转监控
func (t *TLSConfig) startRotation() {
	// 检查是否已有轮转任务
	select {
	case <-t.stopRotation:
		// 创建新的停止通道
		t.stopRotation = make(chan struct{})
	default:
	}

	go t.rotationLoop()
}

// stopRotationMonitor 停止证书轮转监控
func (t *TLSConfig) stopRotationMonitor() {
	close(t.stopRotation)
}

// rotationLoop 证书轮转监控循环
func (t *TLSConfig) rotationLoop() {
	ticker := time.NewTicker(t.RotationInterval)
	defer ticker.Stop()

	// 监控文件系统变化
	watcher, err := t.setupFileWatcher()
	if err != nil {
		log.Printf("设置文件变化监控失败: %v", err)
	}

	// OCSP校验检查器
	var ocspTicker *time.Ticker
	if t.EnableOCSP {
		ocspTicker = time.NewTicker(t.OCSPCheckInterval)
		defer ocspTicker.Stop()
	}

	for {
		select {
		case <-ticker.C:
			// 定期检查证书状态
			if err := t.reloadConfig(); err != nil {
				log.Printf("重新加载TLS配置失败: %v", err)
			}
		case event := <-watcher:
			// 文件系统事件
			if t.isCertificateFile(event) {
				if err := t.reloadConfig(); err != nil {
					log.Printf("文件变化触发重新加载TLS配置失败: %v", err)
				} else {
					log.Printf("检测到证书文件变化，已重新加载TLS配置")
				}
			}
		case <-t.stopRotation:
			return
		case <-t.certChangeChan:
			// 证书已更新
			log.Println("TLS证书已更新")
		}

		if ocspTicker != nil {
			select {
			case <-ocspTicker.C:
				// 刷新OCSP状态
				t.refreshOCSPStatus()
			default:
			}
		}
	}
}

// isCertificateFile 检查文件是否是证书相关文件
func (t *TLSConfig) isCertificateFile(filename string) bool {
	// 检查是否是关心的文件
	return filename == t.CertFile || filename == t.KeyFile || filename == t.CAFile
}

// setupFileWatcher 设置文件变化监控
func (t *TLSConfig) setupFileWatcher() (<-chan string, error) {
	// 这里简化实现，实际应该使用 fsnotify 等库
	// 此处仅返回一个虚拟通道作为示例
	ch := make(chan string)

	// 根据实际需求，可以使用 fsnotify 监控文件变化
	// 此处仅提供基本结构

	return ch, nil
}

// verifyConnectionWithOCSP 使用OCSP校验连接
func (t *TLSConfig) verifyConnectionWithOCSP(state tls.ConnectionState) error {
	leaf := state.PeerCertificates[0]
	// 基础证书验证
	if state.VerifiedChains == nil || len(state.VerifiedChains) == 0 {
		return fmt.Errorf("证书链验证失败")
	}

	if t.EnableOCSP {
		// 获取OCSP服务器URL
		ocspServers := leaf.OCSPServer
		if len(ocspServers) == 0 {
			log.Println("证书不包含OCSP服务器信息，跳过OCSP验证")
			return nil
		}

		issuer := state.VerifiedChains[0][1]
		// 创建OCSP请求
		ocspReq, err := ocsp.CreateRequest(leaf, issuer, nil)
		if err != nil {
			return fmt.Errorf("创建OCSP请求失败: %v", err)
		}

		// 查询OCSP状态
		ocspStatus, err := queryOCSPStatus(ocspServers[0], ocspReq)
		if err != nil {
			log.Printf("OCSP查询失败: %v", err)
			// 如果OCSP查询失败，根据策略可以选择继续或拒绝
			return nil
		}

		if ocspStatus.Status == ocsp.Revoked {
			return fmt.Errorf("证书已被吊销")
		}
	}

	return nil
}

// queryOCSPStatus 查询证书OCSP状态
func queryOCSPStatus(server string, request []byte) (*ocsp.Response, error) {
	// 创建HTTP请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(server, "application/ocsp-request", strings.NewReader(string(request)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 解析OCSP响应
	return ocsp.ParseResponse(body, nil)
}

// refreshOCSPStatus 刷新OCSP状态
func (t *TLSConfig) refreshOCSPStatus() {
	t.mu.RLock()
	config := t.currentConfig
	t.mu.RUnlock()

	if config == nil || !t.EnableOCSP || len(config.Certificates) == 0 {
		return
	}

	// 获取当前证书
	cert := config.Certificates[0]
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		log.Printf("解析证书失败: %v", err)
		return
	}

	// 获取OCSP服务器URL
	ocspServers := leaf.OCSPServer
	if len(ocspServers) == 0 {
		return
	}

	// 解析签发者证书
	var issuer *x509.Certificate
	if len(cert.Certificate) > 1 {
		issuer, err = x509.ParseCertificate(cert.Certificate[1])
		if err != nil {
			log.Printf("解析签发者证书失败: %v", err)
			return
		}
	} else if t.CAFile != "" {
		// 从CA文件加载
		caCert, err := ioutil.ReadFile(t.CAFile)
		if err != nil {
			log.Printf("读取CA证书失败: %v", err)
			return
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			log.Printf("解析CA证书失败")
			return
		}

		caList, err := x509.ParseCertificates(cert.Certificate[0])
		if err != nil || len(caList) == 0 {
			log.Printf("解析CA列表失败: %v", err)
			return
		}

		issuer = caList[0]
	} else {
		log.Println("找不到签发者证书，无法进行OCSP检查")
		return
	}

	// 创建OCSP请求
	ocspReq, err := ocsp.CreateRequest(leaf, issuer, nil)
	if err != nil {
		log.Printf("创建OCSP请求失败: %v", err)
		return
	}

	// 查询OCSP状态
	ocspStatus, err := queryOCSPStatus(ocspServers[0], ocspReq)
	if err != nil {
		log.Printf("OCSP查询失败: %v", err)
		return
	}

	// 处理OCSP响应
	switch ocspStatus.Status {
	case ocsp.Good:
		log.Println("证书状态正常")
	case ocsp.Revoked:
		log.Printf("警告: 证书已被吊销! 证书路径: %s", t.CertFile)
	case ocsp.Unknown:
		log.Printf("证书状态未知")
	}
}

// AutoRotate 配置证书自动轮转
func (t *TLSConfig) AutoRotate(certDir string, certPrefix string) error {
	// 设置证书文件路径
	t.CertFile = filepath.Join(certDir, certPrefix+".crt")
	t.KeyFile = filepath.Join(certDir, certPrefix+".key")

	// 可选CA文件
	caFile := filepath.Join(certDir, "ca.crt")
	if _, err := os.Stat(caFile); err == nil {
		t.CAFile = caFile
	}

	// 初始加载证书
	if err := t.reloadConfig(); err != nil {
		return err
	}

	// 启动轮转监控
	t.startRotation()

	return nil
}

// 生成自签名证书工具函数 (简化版)
func GenerateSelfSignedCert(organization, commonName string, dnsNames []string, ipAddresses []string, validFor time.Duration, outDir string) error {
	// 实际实现应使用crypto/x509库生成证书
	// 此处略去详细实现
	return nil
}
