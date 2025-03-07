package abac

import (
	"context"
	"strings"
	"sync"

	"github.com/dormoron/eidola/auth"
)

// Policy 表示访问策略规则
type Policy struct {
	// Name 策略名称
	Name string

	// Resource 资源名称或模式
	Resource string

	// Action 操作名称或模式
	Action string

	// Condition 条件函数，返回true表示满足
	Condition func(ctx context.Context, user *auth.User, resource, action string) bool
}

// ABACAuthorizer 基于属性的访问控制授权器
type ABACAuthorizer struct {
	policies     []Policy
	mu           sync.RWMutex
	wildcardChar string
}

// ABACOption 授权器选项
type ABACOption func(a *ABACAuthorizer)

// WithWildcardChar 设置通配符
func WithWildcardChar(char string) ABACOption {
	return func(a *ABACAuthorizer) {
		a.wildcardChar = char
	}
}

// NewABACAuthorizer 创建新的ABAC授权器
func NewABACAuthorizer(opts ...ABACOption) *ABACAuthorizer {
	a := &ABACAuthorizer{
		policies:     make([]Policy, 0),
		wildcardChar: "*",
	}

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// AddPolicy 添加策略
func (a *ABACAuthorizer) AddPolicy(policy Policy) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.policies = append(a.policies, policy)
}

// AddPolicies 批量添加策略
func (a *ABACAuthorizer) AddPolicies(policies ...Policy) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.policies = append(a.policies, policies...)
}

// RemovePolicy 移除策略
func (a *ABACAuthorizer) RemovePolicy(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var newPolicies []Policy
	for _, p := range a.policies {
		if p.Name != name {
			newPolicies = append(newPolicies, p)
		}
	}
	a.policies = newPolicies
}

// ClearPolicies 清除所有策略
func (a *ABACAuthorizer) ClearPolicies() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.policies = make([]Policy, 0)
}

// matchPattern 检查字符串是否匹配模式(支持通配符)
func (a *ABACAuthorizer) matchPattern(s, pattern string) bool {
	if pattern == a.wildcardChar {
		return true
	}

	if strings.HasSuffix(pattern, a.wildcardChar) {
		prefix := pattern[:len(pattern)-len(a.wildcardChar)]
		return strings.HasPrefix(s, prefix)
	}

	return s == pattern
}

// Authorize 检查用户是否有权限执行特定操作
func (a *ABACAuthorizer) Authorize(ctx context.Context, user *auth.User, resource string, action string) (bool, error) {
	if user == nil {
		return false, auth.ErrUnauthenticated
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	// 检查是否有匹配的策略
	for _, policy := range a.policies {
		if a.matchPattern(resource, policy.Resource) &&
			a.matchPattern(action, policy.Action) {
			// 如果没有条件或条件满足
			if policy.Condition == nil || policy.Condition(ctx, user, resource, action) {
				return true, nil
			}
		}
	}

	return false, nil
}

// PolicyBuilder 策略构建器
type PolicyBuilder struct {
	name      string
	resource  string
	action    string
	condition func(ctx context.Context, user *auth.User, resource, action string) bool
}

// NewPolicyBuilder 创建新的策略构建器
func NewPolicyBuilder(name string) *PolicyBuilder {
	return &PolicyBuilder{
		name: name,
	}
}

// ForResource 设置资源
func (b *PolicyBuilder) ForResource(resource string) *PolicyBuilder {
	b.resource = resource
	return b
}

// ForAction 设置操作
func (b *PolicyBuilder) ForAction(action string) *PolicyBuilder {
	b.action = action
	return b
}

// WithCondition 设置条件
func (b *PolicyBuilder) WithCondition(condition func(ctx context.Context, user *auth.User, resource, action string) bool) *PolicyBuilder {
	b.condition = condition
	return b
}

// Build 构建策略
func (b *PolicyBuilder) Build() Policy {
	return Policy{
		Name:      b.name,
		Resource:  b.resource,
		Action:    b.action,
		Condition: b.condition,
	}
}

// 常用条件函数

// HasRole 检查用户是否拥有某个角色
func HasRole(role string) func(ctx context.Context, user *auth.User, resource, action string) bool {
	return func(ctx context.Context, user *auth.User, resource, action string) bool {
		for _, r := range user.Roles {
			if r == role {
				return true
			}
		}
		return false
	}
}

// HasAnyRole 检查用户是否拥有任一角色
func HasAnyRole(roles ...string) func(ctx context.Context, user *auth.User, resource, action string) bool {
	return func(ctx context.Context, user *auth.User, resource, action string) bool {
		for _, userRole := range user.Roles {
			for _, role := range roles {
				if userRole == role {
					return true
				}
			}
		}
		return false
	}
}

// HasAllRoles 检查用户是否拥有所有角色
func HasAllRoles(roles ...string) func(ctx context.Context, user *auth.User, resource, action string) bool {
	return func(ctx context.Context, user *auth.User, resource, action string) bool {
		roleMap := make(map[string]bool)
		for _, r := range user.Roles {
			roleMap[r] = true
		}

		for _, role := range roles {
			if !roleMap[role] {
				return false
			}
		}
		return true
	}
}

// HasPermission 检查用户是否拥有权限
func HasPermission(permission string) func(ctx context.Context, user *auth.User, resource, action string) bool {
	return func(ctx context.Context, user *auth.User, resource, action string) bool {
		for _, p := range user.Permissions {
			if p == permission {
				return true
			}
		}
		return false
	}
}

// IsOwner 检查用户是否为资源所有者
func IsOwner(resourceIDExtractor func(string) string) func(ctx context.Context, user *auth.User, resource, action string) bool {
	return func(ctx context.Context, user *auth.User, resource, action string) bool {
		resourceID := resourceIDExtractor(resource)
		return resourceID == user.ID
	}
}

// ResourceBelongsToSameGroup 检查资源是否属于相同组
func ResourceBelongsToSameGroup(userGroupKey, resourceGroupExtractor func(string) string) func(ctx context.Context, user *auth.User, resource, action string) bool {
	return func(ctx context.Context, user *auth.User, resource, action string) bool {
		userGroup := userGroupKey(user.ID)
		resourceGroup := resourceGroupExtractor(resource)
		return userGroup == resourceGroup
	}
}

// And 组合多个条件，全部满足返回true
func And(conditions ...func(ctx context.Context, user *auth.User, resource, action string) bool) func(ctx context.Context, user *auth.User, resource, action string) bool {
	return func(ctx context.Context, user *auth.User, resource, action string) bool {
		for _, condition := range conditions {
			if !condition(ctx, user, resource, action) {
				return false
			}
		}
		return true
	}
}

// Or 组合多个条件，任一满足返回true
func Or(conditions ...func(ctx context.Context, user *auth.User, resource, action string) bool) func(ctx context.Context, user *auth.User, resource, action string) bool {
	return func(ctx context.Context, user *auth.User, resource, action string) bool {
		for _, condition := range conditions {
			if condition(ctx, user, resource, action) {
				return true
			}
		}
		return false
	}
}

// Not 取反条件
func Not(condition func(ctx context.Context, user *auth.User, resource, action string) bool) func(ctx context.Context, user *auth.User, resource, action string) bool {
	return func(ctx context.Context, user *auth.User, resource, action string) bool {
		return !condition(ctx, user, resource, action)
	}
}

// SimpleABACProvider 简单的ABAC提供器
type SimpleABACProvider struct {
	authorizer *ABACAuthorizer
}

// NewSimpleABACProvider 创建新的简单ABAC提供器
func NewSimpleABACProvider() *SimpleABACProvider {
	return &SimpleABACProvider{
		authorizer: NewABACAuthorizer(),
	}
}

// DefaultPolicies 添加默认策略
func (p *SimpleABACProvider) DefaultPolicies() *SimpleABACProvider {
	// 管理员可以做任何事
	p.authorizer.AddPolicy(
		NewPolicyBuilder("admin_all_access").
			ForResource("*").
			ForAction("*").
			WithCondition(HasRole("admin")).
			Build(),
	)

	// 用户可以读取公共资源
	p.authorizer.AddPolicy(
		NewPolicyBuilder("public_read").
			ForResource("public").
			ForAction("read").
			WithCondition(HasAnyRole("user", "guest")).
			Build(),
	)

	// 用户可以读取和更新自己的资源
	p.authorizer.AddPolicy(
		NewPolicyBuilder("self_resource").
			ForResource("user").
			ForAction("*").
			WithCondition(And(
				HasRole("user"),
				IsOwner(func(resource string) string {
					parts := strings.Split(resource, ":")
					if len(parts) > 1 {
						return parts[1]
					}
					return ""
				}),
			)).
			Build(),
	)

	return p
}

// AddPolicy 添加策略
func (p *SimpleABACProvider) AddPolicy(policy Policy) *SimpleABACProvider {
	p.authorizer.AddPolicy(policy)
	return p
}

// Build 构建授权器
func (p *SimpleABACProvider) Build() *ABACAuthorizer {
	return p.authorizer
}
