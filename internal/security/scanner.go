package security

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// 漏洞严重程度
type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
	SeverityLow      Severity = "LOW"
	SeverityInfo     Severity = "INFO"
)

// 漏洞类型
type VulnerabilityType string

const (
	VulnTypeTLS            VulnerabilityType = "TLS_CONFIGURATION"
	VulnTypeAuthentication VulnerabilityType = "AUTHENTICATION"
	VulnTypeAuthorization  VulnerabilityType = "AUTHORIZATION"
	VulnTypeInjection      VulnerabilityType = "INJECTION"
	VulnTypeConfiguration  VulnerabilityType = "CONFIGURATION"
	VulnTypeExposure       VulnerabilityType = "INFORMATION_EXPOSURE"
)

// 漏洞
type Vulnerability struct {
	ID          string            // 漏洞ID
	Type        VulnerabilityType // 漏洞类型
	Title       string            // 标题
	Description string            // 描述
	Severity    Severity          // 严重程度
	Target      string            // 目标（URL, 端点, 文件等）
	Evidence    string            // 证据
	Remediation string            // 修复建议
	References  []string          // 参考资料
	Metadata    map[string]string // 元数据
}

// 检测器接口
type Detector interface {
	// Name 返回检测器名称
	Name() string
	// Description 返回检测器描述
	Description() string
	// Detect 执行检测，返回发现的漏洞
	Detect(ctx context.Context, target interface{}) ([]Vulnerability, error)
}

// 扫描配置
type ScanConfig struct {
	Timeout          time.Duration  // 扫描超时时间
	Concurrency      int            // 并发扫描数量
	TargetHosts      []string       // 目标主机列表
	TargetPorts      []int          // 目标端口列表
	TargetPaths      []string       // 目标路径列表
	TargetFiles      []string       // 目标文件列表
	ExcludeDetectors []string       // 排除的检测器
	IncludeDetectors []string       // 包含的检测器
	MinSeverity      Severity       // 最小严重程度
	Headers          http.Header    // HTTP请求头
	HTTPClient       *http.Client   // HTTP客户端
	CustomParams     map[string]any // 自定义参数
}

// 扫描结果
type ScanResult struct {
	Target          string          // 扫描目标
	Vulnerabilities []Vulnerability // 发现的漏洞
	StartTime       time.Time       // 开始时间
	EndTime         time.Time       // 结束时间
	Duration        time.Duration   // 扫描持续时间
	Error           error           // 扫描错误
}

// 扫描器
type Scanner struct {
	detectors   []Detector
	config      ScanConfig
	resultsChan chan ScanResult
	mu          sync.RWMutex
}

// 创建新的扫描器
func NewScanner(config ScanConfig) *Scanner {
	if config.Concurrency <= 0 {
		config.Concurrency = 10 // 默认并发数
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second // 默认超时时间
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout: config.Timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // 扫描时允许自签名证书
				},
				DisableKeepAlives: true,
			},
		}
	}

	return &Scanner{
		detectors:   make([]Detector, 0),
		config:      config,
		resultsChan: make(chan ScanResult, config.Concurrency),
	}
}

// 注册检测器
func (s *Scanner) RegisterDetector(detector Detector) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detectors = append(s.detectors, detector)
}

// 获取有效的检测器列表
func (s *Scanner) getActiveDetectors() []Detector {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 如果没有指定包含或排除，则使用所有检测器
	if len(s.config.IncludeDetectors) == 0 && len(s.config.ExcludeDetectors) == 0 {
		detectors := make([]Detector, len(s.detectors))
		copy(detectors, s.detectors)
		return detectors
	}

	// 创建排除映射
	excludeMap := make(map[string]bool)
	for _, name := range s.config.ExcludeDetectors {
		excludeMap[name] = true
	}

	// 创建包含映射
	includeMap := make(map[string]bool)
	for _, name := range s.config.IncludeDetectors {
		includeMap[name] = true
	}

	// 过滤检测器
	active := make([]Detector, 0)
	for _, detector := range s.detectors {
		name := detector.Name()
		// 如果在排除列表中，则跳过
		if excludeMap[name] {
			continue
		}
		// 如果有包含列表，且检测器不在其中，则跳过
		if len(includeMap) > 0 && !includeMap[name] {
			continue
		}
		active = append(active, detector)
	}

	return active
}

// 扫描
func (s *Scanner) Scan(ctx context.Context) ([]ScanResult, error) {
	detectors := s.getActiveDetectors()
	if len(detectors) == 0 {
		return nil, errors.New("没有可用的检测器")
	}

	// 创建工作组上下文
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()

	var wg sync.WaitGroup
	results := make([]ScanResult, 0)
	resultsChan := make(chan ScanResult, s.config.Concurrency)

	// 并发处理主机
	semaphore := make(chan struct{}, s.config.Concurrency)
	for _, host := range s.config.TargetHosts {
		for _, port := range s.config.TargetPorts {
			target := fmt.Sprintf("%s:%d", host, port)

			wg.Add(1)
			semaphore <- struct{}{}

			go func(target string) {
				defer wg.Done()
				defer func() { <-semaphore }()

				result := s.scanTarget(ctx, target, detectors)
				resultsChan <- result
			}(target)
		}
	}

	// 并发处理文件
	for _, file := range s.config.TargetFiles {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(file string) {
			defer wg.Done()
			defer func() { <-semaphore }()

			result := s.scanFile(ctx, file, detectors)
			resultsChan <- result
		}(file)
	}

	// 启动收集结果的goroutine
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// 收集结果
	for result := range resultsChan {
		results = append(results, result)
	}

	return results, nil
}

// 扫描目标
func (s *Scanner) scanTarget(ctx context.Context, target string, detectors []Detector) ScanResult {
	startTime := time.Now()
	result := ScanResult{
		Target:          target,
		Vulnerabilities: make([]Vulnerability, 0),
		StartTime:       startTime,
	}

	// 检查是否可以连接
	conn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(startTime)
		result.Error = fmt.Errorf("无法连接到目标: %v", err)
		return result
	}
	conn.Close()

	// 对每个检测器执行检测
	for _, detector := range detectors {
		select {
		case <-ctx.Done():
			result.Error = ctx.Err()
			break
		default:
			vulnerabilities, err := detector.Detect(ctx, target)
			if err != nil {
				continue
			}
			// 过滤低于最小严重程度的漏洞
			if s.config.MinSeverity != "" {
				filtered := make([]Vulnerability, 0)
				for _, vuln := range vulnerabilities {
					if severityLevel(vuln.Severity) >= severityLevel(s.config.MinSeverity) {
						filtered = append(filtered, vuln)
					}
				}
				vulnerabilities = filtered
			}
			result.Vulnerabilities = append(result.Vulnerabilities, vulnerabilities...)
		}
	}

	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(startTime)
	return result
}

// 扫描文件
func (s *Scanner) scanFile(ctx context.Context, filePath string, detectors []Detector) ScanResult {
	startTime := time.Now()
	result := ScanResult{
		Target:          filePath,
		Vulnerabilities: make([]Vulnerability, 0),
		StartTime:       startTime,
	}

	// 读取文件内容
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(startTime)
		result.Error = fmt.Errorf("无法读取文件: %v", err)
		return result
	}

	// 对每个检测器执行检测
	for _, detector := range detectors {
		select {
		case <-ctx.Done():
			result.Error = ctx.Err()
			break
		default:
			vulnerabilities, err := detector.Detect(ctx, content)
			if err != nil {
				continue
			}
			// 过滤低于最小严重程度的漏洞
			if s.config.MinSeverity != "" {
				filtered := make([]Vulnerability, 0)
				for _, vuln := range vulnerabilities {
					if severityLevel(vuln.Severity) >= severityLevel(s.config.MinSeverity) {
						filtered = append(filtered, vuln)
					}
				}
				vulnerabilities = filtered
			}
			result.Vulnerabilities = append(result.Vulnerabilities, vulnerabilities...)
		}
	}

	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(startTime)
	return result
}

// 安全严重程度转换为数字
func severityLevel(severity Severity) int {
	switch severity {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// ======================= TLS配置检测器 =======================

// TLS配置检测器
type TLSConfigDetector struct{}

func NewTLSConfigDetector() *TLSConfigDetector {
	return &TLSConfigDetector{}
}

func (d *TLSConfigDetector) Name() string {
	return "TLSConfigDetector"
}

func (d *TLSConfigDetector) Description() string {
	return "检测TLS配置安全问题，如使用弱加密套件或旧版TLS"
}

func (d *TLSConfigDetector) Detect(ctx context.Context, target interface{}) ([]Vulnerability, error) {
	vulnerabilities := make([]Vulnerability, 0)

	// 检查目标类型
	hostPort, ok := target.(string)
	if !ok {
		return nil, errors.New("目标类型不支持")
	}

	// 解析主机和端口
	_, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, err
	}

	// 创建一个自定义TLS配置
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
	}

	// 测试SSL/TLS版本
	versions := map[uint16]string{
		tls.VersionSSL30: "SSLv3",
		tls.VersionTLS10: "TLSv1.0",
		tls.VersionTLS11: "TLSv1.1",
		tls.VersionTLS12: "TLSv1.2",
		tls.VersionTLS13: "TLSv1.3",
	}

	for version, versionStr := range versions {
		// 跳过TLS 1.2及以上版本
		if version >= tls.VersionTLS12 {
			continue
		}

		config := &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         version,
			MaxVersion:         version,
		}

		conn, err := tls.DialWithDialer(dialer, "tcp", hostPort, config)
		if err == nil {
			// 成功连接，表示服务器支持此版本
			conn.Close()

			var severity Severity
			var title string
			var description string
			var remediation string

			switch version {
			case tls.VersionSSL30:
				severity = SeverityCritical
				title = "支持不安全的SSLv3协议"
				description = "服务器支持已知存在POODLE漏洞的SSLv3协议"
				remediation = "禁用SSLv3，并启用TLS 1.2或更高版本"
			case tls.VersionTLS10:
				severity = SeverityHigh
				title = "支持不安全的TLS 1.0协议"
				description = "服务器支持已知存在多个漏洞的TLS 1.0协议"
				remediation = "禁用TLS 1.0，并启用TLS 1.2或更高版本"
			case tls.VersionTLS11:
				severity = SeverityMedium
				title = "支持不安全的TLS 1.1协议"
				description = "服务器支持已不再推荐使用的TLS 1.1协议"
				remediation = "禁用TLS 1.1，并启用TLS 1.2或更高版本"
			}

			vulnerability := Vulnerability{
				ID:          fmt.Sprintf("TLS-WEAK-VERSION-%s", versionStr),
				Type:        VulnTypeTLS,
				Title:       title,
				Description: description,
				Severity:    severity,
				Target:      hostPort,
				Evidence:    fmt.Sprintf("服务器支持 %s", versionStr),
				Remediation: remediation,
				References: []string{
					"https://nvd.nist.gov/vuln/detail/CVE-2014-3566", // POODLE
					"https://www.openssl.org/~bodo/ssl-poodle.pdf",
					"https://nvd.nist.gov/vuln/detail/CVE-2016-2183", // SWEET32
				},
			}

			vulnerabilities = append(vulnerabilities, vulnerability)
		}
	}

	// 检测证书问题
	config := &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	}
	conn, err := tls.DialWithDialer(dialer, "tcp", hostPort, config)
	if err == nil {
		defer conn.Close()

		certs := conn.ConnectionState().PeerCertificates
		if len(certs) > 0 {
			cert := certs[0]

			// 检查证书是否过期
			now := time.Now()
			if now.After(cert.NotAfter) {
				vulnerability := Vulnerability{
					ID:          "TLS-CERT-EXPIRED",
					Type:        VulnTypeTLS,
					Title:       "SSL/TLS证书已过期",
					Description: fmt.Sprintf("服务器证书已于 %s 过期", cert.NotAfter.Format("2006-01-02")),
					Severity:    SeverityHigh,
					Target:      hostPort,
					Evidence:    fmt.Sprintf("证书过期时间: %s", cert.NotAfter.Format("2006-01-02")),
					Remediation: "更新SSL/TLS证书",
				}
				vulnerabilities = append(vulnerabilities, vulnerability)
			} else if now.Add(30 * 24 * time.Hour).After(cert.NotAfter) {
				// 证书将在30天内过期
				vulnerability := Vulnerability{
					ID:          "TLS-CERT-EXPIRING-SOON",
					Type:        VulnTypeTLS,
					Title:       "SSL/TLS证书即将过期",
					Description: fmt.Sprintf("服务器证书将在 %s 过期", cert.NotAfter.Format("2006-01-02")),
					Severity:    SeverityMedium,
					Target:      hostPort,
					Evidence:    fmt.Sprintf("证书过期时间: %s", cert.NotAfter.Format("2006-01-02")),
					Remediation: "更新SSL/TLS证书",
				}
				vulnerabilities = append(vulnerabilities, vulnerability)
			}

			// 检查自签名证书
			if len(certs) == 1 && cert.Issuer.String() == cert.Subject.String() {
				vulnerability := Vulnerability{
					ID:          "TLS-CERT-SELF-SIGNED",
					Type:        VulnTypeTLS,
					Title:       "使用自签名SSL/TLS证书",
					Description: "服务器使用自签名证书，这可能导致中间人攻击",
					Severity:    SeverityMedium,
					Target:      hostPort,
					Evidence:    "证书颁发者与主题相同",
					Remediation: "使用受信任证书颁发机构签发的证书",
				}
				vulnerabilities = append(vulnerabilities, vulnerability)
			}
		}
	}

	return vulnerabilities, nil
}

// ======================= 配置漏洞检测器 =======================

// 配置漏洞检测器
type ConfigDetector struct{}

func NewConfigDetector() *ConfigDetector {
	return &ConfigDetector{}
}

func (d *ConfigDetector) Name() string {
	return "ConfigDetector"
}

func (d *ConfigDetector) Description() string {
	return "检测配置文件中的安全漏洞，如敏感信息泄露"
}

func (d *ConfigDetector) Detect(ctx context.Context, target interface{}) ([]Vulnerability, error) {
	vulnerabilities := make([]Vulnerability, 0)

	// 检查目标类型
	content, ok := target.([]byte)
	if !ok {
		return nil, errors.New("目标类型不支持")
	}

	// 定义敏感信息模式
	patterns := map[string]struct {
		regex       *regexp.Regexp
		title       string
		description string
		severity    Severity
	}{
		"AWS_ACCESS_KEY": {
			regex:       regexp.MustCompile(`(?i)(aws_access_key_id|aws_access_key)\s*[:=]\s*['"]?([A-Z0-9]{20})['"]?`),
			title:       "暴露的AWS访问密钥",
			description: "文件包含AWS访问密钥",
			severity:    SeverityHigh,
		},
		"AWS_SECRET_KEY": {
			regex:       regexp.MustCompile(`(?i)(aws_secret_access_key|aws_secret_key)\s*[:=]\s*['"]?([A-Za-z0-9/+]{40})['"]?`),
			title:       "暴露的AWS密钥",
			description: "文件包含AWS密钥",
			severity:    SeverityHigh,
		},
		"API_KEY": {
			regex:       regexp.MustCompile(`(?i)(api_key|apikey|api-key)\s*[:=]\s*['"]?([A-Za-z0-9]{32,})['"]?`),
			title:       "暴露的API密钥",
			description: "文件包含API密钥",
			severity:    SeverityHigh,
		},
		"PASSWORD": {
			regex:       regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*['"]?([^'"\s]{8,})['"]?`),
			title:       "暴露的密码",
			description: "文件包含明文密码",
			severity:    SeverityHigh,
		},
		"PRIVATE_KEY": {
			regex:       regexp.MustCompile(`(?i)-----BEGIN\s+PRIVATE\s+KEY-----`),
			title:       "暴露的私钥",
			description: "文件包含私钥",
			severity:    SeverityCritical,
		},
		"OAUTH_TOKEN": {
			regex:       regexp.MustCompile(`(?i)(oauth_token|oauth-token)\s*[:=]\s*['"]?([A-Za-z0-9_\-.]{30,})['"]?`),
			title:       "暴露的OAuth令牌",
			description: "文件包含OAuth令牌",
			severity:    SeverityHigh,
		},
	}

	// 检查每个模式
	for key, pattern := range patterns {
		matches := pattern.regex.FindAllSubmatch(content, -1)
		for _, match := range matches {
			// 提取匹配的内容
			var evidence string
			if len(match) > 1 {
				if len(match[0]) > 50 {
					evidence = string(match[0][:50]) + "..."
				} else {
					evidence = string(match[0])
				}
			}

			vulnerability := Vulnerability{
				ID:          fmt.Sprintf("CONFIG-SENSITIVE-INFO-%s", key),
				Type:        VulnTypeExposure,
				Title:       pattern.title,
				Description: pattern.description,
				Severity:    pattern.severity,
				Target:      string(match[1]),
				Evidence:    evidence,
				Remediation: "移除或加密敏感信息，使用环境变量或安全的密钥管理服务",
				References: []string{
					"https://owasp.org/www-project-top-ten/2017/A3_2017-Sensitive_Data_Exposure",
				},
			}

			vulnerabilities = append(vulnerabilities, vulnerability)
		}
	}

	// 检查明文证书
	if bytes.Contains(content, []byte("-----BEGIN CERTIFICATE-----")) {
		vulnerability := Vulnerability{
			ID:          "CONFIG-CERTIFICATE-EXPOSURE",
			Type:        VulnTypeExposure,
			Title:       "暴露的证书",
			Description: "文件包含证书",
			Severity:    SeverityMedium,
			Target:      "证书文件",
			Evidence:    "包含 -----BEGIN CERTIFICATE----- 标记",
			Remediation: "确保证书文件有适当的访问控制",
		}
		vulnerabilities = append(vulnerabilities, vulnerability)
	}

	// 检查硬编码的IP地址
	ipRegex := regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipMatches := ipRegex.FindAll(content, -1)
	for _, match := range ipMatches {
		ip := string(match)
		// 排除常见的无效IP
		if ip != "127.0.0.1" && ip != "0.0.0.0" && !strings.HasPrefix(ip, "192.168.") && !strings.HasPrefix(ip, "10.") {
			vulnerability := Vulnerability{
				ID:          "CONFIG-HARDCODED-IP",
				Type:        VulnTypeConfiguration,
				Title:       "硬编码的IP地址",
				Description: "文件包含硬编码的IP地址",
				Severity:    SeverityLow,
				Target:      ip,
				Evidence:    fmt.Sprintf("包含IP地址: %s", ip),
				Remediation: "使用配置文件或环境变量来存储IP地址",
			}
			vulnerabilities = append(vulnerabilities, vulnerability)
		}
	}

	return vulnerabilities, nil
}

// ======================= 创建默认扫描器 =======================

// 创建默认扫描器，注册所有检测器
func NewDefaultScanner(config ScanConfig) *Scanner {
	scanner := NewScanner(config)

	// 注册检测器
	scanner.RegisterDetector(NewTLSConfigDetector())
	scanner.RegisterDetector(NewConfigDetector())

	return scanner
}
