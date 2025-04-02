// Package mfa 提供了多因素认证(MFA)的功能支持。
//
// MFA是一种安全认证方法，要求用户提供不同类型的身份验证因素进行身份验证，以提高安全性。
// 本包提供了一个灵活的MFA框架，支持多种MFA方法，包括TOTP(基于时间的一次性密码)。
//
// 主要组件:
//   - MFAProvider: MFA提供者接口，定义了MFA方法的基本操作。
//   - TOTPProvider: 实现了基于时间的一次性密码(TOTP)的MFA提供者。
//   - MFARepository: MFA存储接口，用于存储用户的MFA配置。
//   - InMemoryMFARepository: 内存实现的MFA存储，适用于开发和测试环境。
//   - RedisMFARepository: 基于Redis的MFA存储实现，适用于生产环境。
//   - MFAAuthenticator: 多因素认证器，整合基础认证和MFA认证。
//
// 使用示例:
//
//	// 创建TOTP提供者
//	totpProvider := mfa.NewTOTPProvider(repository, "MyApp", 1)
//
//	// 创建MFA认证器
//	mfaAuth := mfa.NewMFAAuthenticator(mfa.MFAAuthenticatorConfig{
//		BaseAuthenticator: baseAuth,
//		Providers:         []mfa.MFAProvider{totpProvider},
//		Repository:        repository,
//		ForceMFA:          false,
//	})
//
//	// 第一阶段认证
//	result, err := mfaAuth.PreAuthenticate(ctx, credential)
//	if err != nil {
//		// 处理错误
//	}
//
//	// 检查是否需要MFA
//	if result.RequireMFA {
//		// 第二阶段认证
//		valid, err := mfaAuth.CompleteMFA(ctx, userID, mfa.MFATypeTOTP, code)
//		// ...
//	}
//
// Redis存储示例:
//
//	// 创建Redis MFA仓库
//	mfaRepo, err := mfa.NewRedisMFARepository(mfa.RedisMFARepositoryConfig{
//		Client:     redisClient,          // 实现了RedisMFAClient接口的客户端
//		KeyPrefix:  "mfa:",               // 键前缀
//		Expiration: time.Hour * 24 * 365, // 数据过期时间
//	})
//
// TOTP支持:
// 本包提供了完整的TOTP实现，符合RFC 6238标准，支持:
//   - 生成随机密钥
//   - 生成和验证TOTP码
//   - 生成二维码URI
//   - 可配置的算法、位数、周期等参数
//
// MFA设置流程:
// 1. 调用SetupMFA获取初始设置信息
// 2. 用户配置认证器应用(如Google Authenticator)
// 3. 用户输入验证码，调用SetupMFA进行验证
// 4. 验证成功，MFA设置完成
//
// MFA认证流程:
// 1. 调用PreAuthenticate进行基础认证
// 2. 如果RequireMFA为true，引导用户使用认证器生成验证码
// 3. 调用CompleteMFA验证用户提供的验证码
// 4. 验证成功，认证完成
//
// 持久化存储:
// 框架支持两种MFA数据存储方式:
//   - InMemoryMFARepository: 内存存储，适用于开发和测试环境
//   - RedisMFARepository: Redis存储，适用于生产环境，提供持久化和集群支持
package mfa
