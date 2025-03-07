package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/dormoron/eidola/auth"
	"github.com/dormoron/eidola/auth/abac"
	"github.com/dormoron/eidola/auth/basic"
	"github.com/dormoron/eidola/auth/jwt"
	"github.com/dormoron/eidola/auth/rbac"
)

const (
	// JWT密钥
	jwtSecret = "my-super-secret-key-for-eidola-framework"
)

// Greeter 自定义问候服务接口
type Greeter interface {
	// SayHello 问候方法
	SayHello(ctx context.Context, name string) (string, error)
}

// UserService 实现问候服务接口
type UserService struct {
	Greeter
}

// SayHello 实现SayHello方法
func (s *UserService) SayHello(ctx context.Context, name string) (string, error) {
	// 从上下文中获取用户信息
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("用户未认证")
	}

	// 使用用户信息构建响应
	message := fmt.Sprintf("你好 %s (用户: %s, 角色: %v)", name, user.Name, user.Roles)
	return message, nil
}

// AdminService 管理服务
type AdminService struct {
	Greeter
}

// SayHello 实现SayHello方法
func (s *AdminService) SayHello(ctx context.Context, name string) (string, error) {
	// 从上下文中获取用户信息
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("用户未认证")
	}

	// 检查用户是否有管理员角色
	hasAdminRole := false
	for _, role := range user.Roles {
		if role == "admin" {
			hasAdminRole = true
			break
		}
	}

	if !hasAdminRole {
		return "", fmt.Errorf("需要管理员权限")
	}

	// 使用用户信息构建响应
	message := fmt.Sprintf("管理员你好 %s (用户: %s, 角色: %v)", name, user.Name, user.Roles)
	return message, nil
}

// 创建JWT认证提供器
func createJWTAuthProvider() auth.AuthProvider {
	// 创建JWT令牌管理器
	tokenManager := jwt.NewJWTManager([]byte(jwtSecret), jwt.JWTConfig{
		Expiry:   time.Hour,
		Issuer:   "eidola-example",
		Audience: "example-service",
	})

	// 创建JWT认证器
	authenticator := jwt.NewJWTAuthenticator(tokenManager)

	// 创建基于角色的访问控制
	rbacAuthorizer := rbac.NewSimpleRBACProvider().DefaultRoles().Build()

	// 创建基于属性的访问控制
	abacAuthorizer := abac.NewSimpleABACProvider().DefaultPolicies().Build()

	// 创建多策略授权器，任一授权即可
	authorizer := auth.NewMultiAuthorizer("any", rbacAuthorizer, abacAuthorizer)

	// 创建令牌提取器
	extractor := jwt.NewHeaderTokenExtractor("")

	// 创建认证提供器
	return auth.NewAuthProviderFactory().
		WithAuthenticator(authenticator).
		WithAuthorizer(authorizer).
		WithTokenManager(tokenManager).
		AddExtractor(extractor).
		Build()
}

// 创建基本认证提供器
func createBasicAuthProvider() auth.AuthProvider {
	// 创建内存用户存储
	userStore := basic.NewInMemoryUserStore()

	// 添加用户
	userStore.AddUser("user", "password", &auth.User{
		ID:          "1",
		Name:        "普通用户",
		Roles:       []string{"user"},
		Permissions: []string{"user:read", "user:update_self"},
	})

	userStore.AddUser("admin", "admin123", &auth.User{
		ID:          "2",
		Name:        "管理员",
		Roles:       []string{"admin", "user"},
		Permissions: []string{"*:*"},
	})

	// 创建基本认证器
	authenticator := basic.NewBasicAuthenticator(userStore)

	// 创建基于角色的访问控制
	authorizer := rbac.NewSimpleRBACProvider().DefaultRoles().Build()

	// 创建令牌提取器
	extractor := basic.NewHeaderBasicExtractor("")

	// 创建认证提供器
	return auth.NewAuthProviderFactory().
		WithAuthenticator(authenticator).
		WithAuthorizer(authorizer).
		AddExtractor(extractor).
		Build()
}

// 演示认证授权
func demoAuthFlow() {
	log.Println("============ Eidola 认证与授权演示 ============")

	// 1. 创建认证提供器
	authProvider := createBasicAuthProvider()

	// 也可以使用JWT认证
	// authProvider := createJWTAuthProvider()

	// 2. 获取授权器
	authorizer := authProvider.GetAuthorizer()

	// 3. 获取认证器
	authenticator := authProvider.GetAuthenticator()

	// 4. 创建用户凭证
	credentials := basic.NewBasicCredentials("admin", "admin123")

	// 5. 认证用户
	user, err := authenticator.Authenticate(context.Background(), credentials)
	if err != nil {
		log.Fatalf("认证失败: %v", err)
	}

	log.Printf("认证成功: 用户 %s (ID: %s, 角色: %v)", user.Name, user.ID, user.Roles)

	// 6. 检查权限
	allowed, err := authorizer.Authorize(context.Background(), user, "admin", "access")
	if err != nil {
		log.Fatalf("授权检查失败: %v", err)
	}

	if allowed {
		log.Printf("授权成功: 用户 %s 有权限执行 %s:%s", user.Name, "admin", "access")
	} else {
		log.Printf("授权失败: 用户 %s 没有权限执行 %s:%s", user.Name, "admin", "access")
	}

	// 7. 使用上下文
	ctx := auth.WithUser(context.Background(), user)

	// 8. 创建服务
	userService := &UserService{}
	adminService := &AdminService{}

	// 9. 调用服务
	message, err := userService.SayHello(ctx, "测试用户")
	if err != nil {
		log.Printf("用户服务调用失败: %v", err)
	} else {
		log.Printf("用户服务响应: %s", message)
	}

	message, err = adminService.SayHello(ctx, "测试管理员")
	if err != nil {
		log.Printf("管理员服务调用失败: %v", err)
	} else {
		log.Printf("管理员服务响应: %s", message)
	}

	// 10. 测试无权限场景
	// 创建普通用户凭证
	userCredentials := basic.NewBasicCredentials("user", "password")

	// 认证普通用户
	normalUser, err := authenticator.Authenticate(context.Background(), userCredentials)
	if err != nil {
		log.Fatalf("普通用户认证失败: %v", err)
	}

	// 将普通用户添加到上下文
	userCtx := auth.WithUser(context.Background(), normalUser)

	// 尝试调用管理员服务
	message, err = adminService.SayHello(userCtx, "测试普通用户")
	if err != nil {
		log.Printf("预期的权限错误: %v", err)
	} else {
		log.Printf("异常: 普通用户能够访问管理员服务: %s", message)
	}
}

func main() {
	// 解析命令行参数
	advancedMode := flag.Bool("advanced", false, "运行高级JWT功能示例")
	flag.Parse()

	if *advancedMode {
		// 运行高级示例
		runAdvancedDemo()
	} else {
		// 运行基础示例
		runBasicDemo()
	}
}

// runBasicDemo 运行基础JWT示例
func runBasicDemo() {
	// 1. 创建JWT管理器
	jwtManager := jwt.NewJWTManager([]byte("example-signing-key"))

	// 2. 创建用户
	user := &auth.User{
		ID:   "user123",
		Name: "测试用户",
		Roles: []string{
			"admin",
			"user",
		},
		Permissions: []string{
			"read",
			"write",
			"delete",
		},
		Metadata: map[string]interface{}{
			"email":      "test@example.com",
			"department": "IT",
		},
	}

	// 3. 生成令牌
	token, err := jwtManager.CreateToken(user, time.Hour)
	if err != nil {
		log.Fatalf("生成令牌失败: %v", err)
	}
	fmt.Printf("生成的令牌: %s\n", token)

	// 4. 验证令牌
	claims, err := jwtManager.ValidateToken(token)
	if err != nil {
		log.Fatalf("验证令牌失败: %v", err)
	}

	fmt.Println("验证成功！令牌中的信息:")
	fmt.Printf("用户ID: %s\n", claims["uid"].(string))
	fmt.Printf("用户名: %s\n", claims["username"].(string))
	fmt.Printf("角色: %v\n", claims["roles"].([]string))
	fmt.Printf("权限: %v\n", claims["permissions"].([]string))

	// 5. 创建认证器
	authenticator := jwt.NewJWTAuthenticator(jwtManager)

	// 6. 认证用户
	creds := jwt.NewJWTCredentials(token)
	authenticatedUser, err := authenticator.Authenticate(context.Background(), creds)
	if err != nil {
		log.Fatalf("认证失败: %v", err)
	}

	fmt.Println("\n认证成功！用户信息:")
	fmt.Printf("用户ID: %s\n", authenticatedUser.ID)
	fmt.Printf("用户名: %s\n", authenticatedUser.Name)
	fmt.Printf("角色: %v\n", authenticatedUser.Roles)
	fmt.Printf("权限: %v\n", authenticatedUser.Permissions)

	// 7. 检查授权
	hasRole := false
	for _, role := range authenticatedUser.Roles {
		if role == "admin" {
			hasRole = true
			break
		}
	}

	if hasRole {
		fmt.Println("\n用户拥有admin角色，允许管理操作")
	} else {
		fmt.Println("\n用户没有admin角色，禁止管理操作")
	}

	hasPermission := false
	for _, perm := range authenticatedUser.Permissions {
		if perm == "delete" {
			hasPermission = true
			break
		}
	}

	if hasPermission {
		fmt.Println("用户拥有delete权限，允许删除操作")
	} else {
		fmt.Println("用户没有delete权限，禁止删除操作")
	}
}

// runAdvancedDemo 运行高级JWT功能示例
func runAdvancedDemo() {
	fmt.Println("Eidola JWT高级功能示例")
	fmt.Println("===========================")

	// 创建基础用户
	user := &auth.User{
		ID:          "user123",
		Name:        "测试用户",
		Roles:       []string{"user", "admin"},
		Permissions: []string{"read", "write", "delete"},
		Metadata: map[string]interface{}{
			"email": "test@example.com",
			"age":   30,
		},
	}

	// 1. 设置JWT配置
	jwtConfig := jwt.JWTConfig{
		SigningMethod: jwt.DefaultJWTConfig.SigningMethod,
		Expiry:        time.Minute * 5, // 短期令牌，便于测试过期
		RefreshExpiry: time.Hour * 24,
		Issuer:        "eidola-example",
		Audience:      "example-app",
	}

	// 2. 演示：令牌轮换功能
	fmt.Println("\n1. 令牌轮换功能演示")
	fmt.Println("----------------------------")

	// 创建内存密钥存储
	keyStorage := jwt.NewMemoryKeyStorage()

	// 创建令牌轮换器
	rotator := jwt.NewTokenRotator(
		keyStorage,       // 密钥存储
		32,               // 密钥长度（字节）
		jwt.RotateHybrid, // 轮换策略
		time.Minute*10,   // 轮换间隔
		time.Minute*2,    // 密钥重叠期
	)

	// 启动令牌轮换器
	err := rotator.Start(context.Background())
	if err != nil {
		log.Fatalf("启动令牌轮换器失败: %v", err)
	}
	defer rotator.Stop()

	// 创建带轮换的JWT管理器
	rotatingManager := jwt.NewRotatingJWTManager(rotator, jwtConfig)
	err = rotatingManager.Init(context.Background())
	if err != nil {
		log.Fatalf("初始化轮换JWT管理器失败: %v", err)
	}

	// 生成令牌
	rotatingToken, err := rotatingManager.GenerateToken(user)
	if err != nil {
		log.Fatalf("生成轮换令牌失败: %v", err)
	}

	fmt.Printf("生成的轮换令牌: %s\n", rotatingToken)

	// 验证令牌
	rotatingClaims, err := rotatingManager.ValidateToken(rotatingToken)
	if err != nil {
		log.Fatalf("验证轮换令牌失败: %v", err)
	}

	fmt.Println("轮换令牌验证成功，用户信息:")
	fmt.Printf("  用户ID: %s\n", rotatingClaims["uid"])
	fmt.Printf("  用户名: %s\n", rotatingClaims["username"])
	fmt.Printf("  角色: %v\n", rotatingClaims["roles"])

	// 执行令牌轮换
	fmt.Println("\n立即执行令牌轮换...")
	err = rotator.RotateNow(context.Background())
	if err != nil {
		log.Fatalf("执行令牌轮换失败: %v", err)
	}

	// 生成新令牌（使用新密钥）
	newRotatingToken, err := rotatingManager.GenerateToken(user)
	if err != nil {
		log.Fatalf("生成新轮换令牌失败: %v", err)
	}

	fmt.Printf("轮换后生成的新令牌: %s\n", newRotatingToken)

	// 原令牌仍然有效（密钥重叠期）
	_, err = rotatingManager.ValidateToken(rotatingToken)
	if err != nil {
		fmt.Printf("原令牌验证失败: %v\n", err)
	} else {
		fmt.Println("原令牌仍然有效（密钥重叠期）")
	}

	// 3. 演示：黑名单功能
	fmt.Println("\n2. 黑名单功能演示")
	fmt.Println("----------------------------")

	// 创建标准JWT管理器（不带轮换）
	staticManager := jwt.NewJWTManager([]byte("example-static-key"), jwtConfig)

	// 创建令牌
	staticToken, err := staticManager.CreateToken(user, 0)
	if err != nil {
		log.Fatalf("生成静态令牌失败: %v", err)
	}

	fmt.Printf("生成的静态令牌: %s\n", staticToken)

	// 创建内存黑名单存储
	blacklistStorage := jwt.NewMemoryBlacklistStorage(time.Minute * 5)

	// 创建黑名单
	blacklist := jwt.NewTokenBlacklist(staticManager, blacklistStorage)

	// 创建黑名单认证器
	blacklistAuthenticator := jwt.NewBlacklistedJWTAuthenticator(staticManager, blacklist)

	// 在未加入黑名单前验证令牌
	creds := jwt.NewJWTCredentials(staticToken)
	validUser, err := blacklistAuthenticator.Authenticate(context.Background(), creds)
	if err != nil {
		fmt.Printf("验证失败: %v\n", err)
	} else {
		fmt.Println("令牌验证成功:")
		fmt.Printf("  用户ID: %s\n", validUser.ID)
		fmt.Printf("  用户名: %s\n", validUser.Name)
	}

	// 将令牌加入黑名单
	fmt.Println("\n将令牌加入黑名单...")
	err = blacklist.Add(context.Background(), staticToken)
	if err != nil {
		log.Fatalf("将令牌加入黑名单失败: %v", err)
	}

	// 再次验证，应该失败
	_, err = blacklistAuthenticator.Authenticate(context.Background(), creds)
	if err != nil {
		fmt.Printf("令牌已列入黑名单，验证失败: %v\n", err)
	} else {
		fmt.Println("令牌验证成功（不应该发生）")
	}

	// 4. 演示：增强验证功能
	fmt.Println("\n3. 增强验证功能演示")
	fmt.Println("----------------------------")

	// 创建新令牌（不在黑名单中）
	user2 := &auth.User{
		ID:          "user456",
		Name:        "另一个用户",
		Roles:       []string{"user"},
		Permissions: []string{"read"},
		Metadata: map[string]interface{}{
			"email": "test@example.com",
		},
	}
	validToken, err := staticManager.CreateToken(user2, 0)
	if err != nil {
		fmt.Printf("生成令牌失败: %v，继续执行示例\n", err)
		validToken = "dummy-token" // 使用虚拟令牌继续演示
	}

	// 创建带增强验证的认证器
	enhancedValidator := jwt.NewEnhancedValidator(staticManager, blacklist)

	// 添加自定义验证规则
	enhancedValidator.AddRule(jwt.CustomClaimValidator("email", "test@example.com", true))
	enhancedValidator.AddRule(jwt.IssuedAtValidator(time.Hour * 24))

	// 创建带增强验证的认证器
	enhancedAuthenticator := jwt.NewEnhancedJWTAuthenticator(enhancedValidator)

	// 验证令牌
	validCreds := jwt.NewJWTCredentials(validToken)
	enhancedUser, err := enhancedAuthenticator.Authenticate(context.Background(), validCreds)
	if err != nil {
		fmt.Printf("增强验证失败: %v，继续执行示例\n", err)
	} else {
		fmt.Println("增强验证成功:")
		fmt.Printf("  用户ID: %s\n", enhancedUser.ID)
		fmt.Printf("  用户名: %s\n", enhancedUser.Name)
		fmt.Printf("  Email: %s\n", enhancedUser.Metadata["email"])
	}

	// 测试验证规则：必须包含特定角色
	fmt.Println("\n添加角色验证规则...")
	enhancedValidator.AddRule(jwt.CustomClaimValidator("roles", []string{"admin"}, true))

	// 再次验证，应该失败（缺少admin角色）
	_, err = enhancedAuthenticator.Authenticate(context.Background(), validCreds)
	if err != nil {
		fmt.Printf("角色验证失败: %v\n", err)
	} else {
		fmt.Println("角色验证成功（不应该发生）")
	}

	// 5. 演示：令牌自动刷新
	fmt.Println("\n4. 令牌自动刷新功能演示")
	fmt.Println("----------------------------")

	// 创建令牌刷新器
	refresher := jwt.NewTokenRefresher(staticManager, jwt.WithRefreshThreshold(time.Minute*4)) // 在过期前4分钟刷新
	err = refresher.Start(context.Background())
	if err != nil {
		fmt.Printf("启动令牌刷新器失败: %v，继续执行示例\n", err)
	} else {
		defer refresher.Stop()

		// 创建客户端
		client := jwt.NewAutoRefreshClient(validToken, refresher)

		// 注册令牌
		refreshed := false
		refreshCh, err := client.RegisterToken(func(oldToken, newToken string) {
			fmt.Printf("令牌已自动刷新\n")
			fmt.Printf("  旧令牌: %s\n", oldToken)
			fmt.Printf("  新令牌: %s\n", newToken)
			refreshed = true
		})
		if err != nil {
			fmt.Printf("注册令牌失败: %v，继续执行示例\n", err)
		} else {
			defer client.UnregisterToken(refreshCh)

			// 获取当前令牌
			currentToken := client.GetCurrentToken()
			fmt.Printf("当前令牌: %s\n", currentToken)

			// 等待令牌即将过期（模拟）
			fmt.Println("\n手动触发令牌刷新...")
			refreshedToken, refreshOccurred, err := refresher.CheckAndRefreshToken(context.Background(), currentToken, time.Now().Add(time.Minute*4-time.Second*1))
			if err != nil {
				fmt.Printf("刷新令牌失败: %v\n", err)
			} else if refreshOccurred {
				fmt.Printf("令牌已刷新: %s\n", refreshedToken)
			} else {
				fmt.Println("令牌未刷新")
			}

			// 等待刷新完成
			time.Sleep(time.Second * 1)

			if refreshed {
				fmt.Println("令牌已成功通过回调刷新")
			}
		}
	}

	fmt.Println("\n示例程序完成！")
}
