package rbac

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/dormoron/eidola/auth"
)

// Permission 表示对资源的操作权限
type Permission struct {
	Resource string
	Action   string
}

// String 返回权限的字符串表示
func (p Permission) String() string {
	return fmt.Sprintf("%s:%s", p.Resource, p.Action)
}

// ParsePermission 解析权限字符串
func ParsePermission(s string) (Permission, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return Permission{}, fmt.Errorf("invalid permission format: %s", s)
	}
	return Permission{
		Resource: parts[0],
		Action:   parts[1],
	}, nil
}

// Role 表示角色及其拥有的权限
type Role struct {
	Name        string
	Permissions []Permission
	Parents     []string
}

// RBACAuthorizer 基于角色的访问控制授权器
type RBACAuthorizer struct {
	roles        map[string]*Role
	userRoles    map[string][]string
	mu           sync.RWMutex
	wildcardChar string
}

// NewRBACAuthorizer 创建新的RBAC授权器
func NewRBACAuthorizer(opts ...RBACOption) *RBACAuthorizer {
	a := &RBACAuthorizer{
		roles:        make(map[string]*Role),
		userRoles:    make(map[string][]string),
		wildcardChar: "*",
	}

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// RBACOption 授权器选项
type RBACOption func(a *RBACAuthorizer)

// WithWildcardChar 设置通配符字符
func WithWildcardChar(char string) RBACOption {
	return func(a *RBACAuthorizer) {
		a.wildcardChar = char
	}
}

// AddRole 添加角色
func (a *RBACAuthorizer) AddRole(name string, permissions []Permission, parents ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.roles[name] = &Role{
		Name:        name,
		Permissions: permissions,
		Parents:     parents,
	}
}

// AddRolePermission 向角色添加权限
func (a *RBACAuthorizer) AddRolePermission(role string, perm Permission) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if r, exists := a.roles[role]; exists {
		// 检查权限是否已存在
		for _, p := range r.Permissions {
			if p.Resource == perm.Resource && p.Action == perm.Action {
				return
			}
		}
		r.Permissions = append(r.Permissions, perm)
	}
}

// AssignRoleToUser 将角色分配给用户
func (a *RBACAuthorizer) AssignRoleToUser(userID string, roles ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.userRoles[userID] = append(a.userRoles[userID], roles...)
}

// RemoveRoleFromUser 从用户中移除角色
func (a *RBACAuthorizer) RemoveRoleFromUser(userID string, role string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	userRoles, exists := a.userRoles[userID]
	if !exists {
		return
	}

	var newRoles []string
	for _, r := range userRoles {
		if r != role {
			newRoles = append(newRoles, r)
		}
	}

	a.userRoles[userID] = newRoles
}

// ClearUserRoles 清除用户所有角色
func (a *RBACAuthorizer) ClearUserRoles(userID string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.userRoles, userID)
}

// GetUserRoles 获取用户角色
func (a *RBACAuthorizer) GetUserRoles(userID string) []string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	roles, exists := a.userRoles[userID]
	if !exists {
		return nil
	}

	return append([]string{}, roles...)
}

// hasPermission 检查角色是否有特定权限
func (a *RBACAuthorizer) hasPermission(roleName string, resource, action string, visited map[string]bool) bool {
	// 避免循环依赖
	if visited[roleName] {
		return false
	}
	visited[roleName] = true

	role, exists := a.roles[roleName]
	if !exists {
		return false
	}

	// 检查直接权限
	for _, perm := range role.Permissions {
		// 检查资源匹配
		if perm.Resource == resource || perm.Resource == a.wildcardChar {
			// 检查操作匹配
			if perm.Action == action || perm.Action == a.wildcardChar {
				return true
			}
		}
	}

	// 检查父角色权限
	for _, parent := range role.Parents {
		if a.hasPermission(parent, resource, action, visited) {
			return true
		}
	}

	return false
}

// Authorize 检查用户是否有权限执行特定操作
func (a *RBACAuthorizer) Authorize(ctx context.Context, user *auth.User, resource string, action string) (bool, error) {
	if user == nil {
		return false, auth.ErrUnauthenticated
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	// 如果用户对象中有角色，使用用户对象中的角色
	roles := user.Roles
	if len(roles) == 0 {
		// 否则，从授权器中获取用户角色
		roles = a.userRoles[user.ID]
	}

	// 直接检查用户权限
	for _, perm := range user.Permissions {
		permObj, err := ParsePermission(perm)
		if err != nil {
			continue
		}
		if (permObj.Resource == resource || permObj.Resource == a.wildcardChar) &&
			(permObj.Action == action || permObj.Action == a.wildcardChar) {
			return true, nil
		}
	}

	// 检查角色权限
	for _, role := range roles {
		if a.hasPermission(role, resource, action, make(map[string]bool)) {
			return true, nil
		}
	}

	return false, nil
}

// SimpleRBACProvider 简单的RBAC提供器，可以用于快速实现基于角色的授权
type SimpleRBACProvider struct {
	authorizer *RBACAuthorizer
}

// NewSimpleRBACProvider 创建新的简单RBAC提供器
func NewSimpleRBACProvider() *SimpleRBACProvider {
	return &SimpleRBACProvider{
		authorizer: NewRBACAuthorizer(),
	}
}

// DefaultRoles 设置默认角色和权限
func (p *SimpleRBACProvider) DefaultRoles() *SimpleRBACProvider {
	// 管理员角色，拥有所有权限
	p.authorizer.AddRole("admin", []Permission{
		{Resource: "*", Action: "*"},
	})

	// 用户角色，拥有基本权限
	p.authorizer.AddRole("user", []Permission{
		{Resource: "user", Action: "read"},
		{Resource: "user", Action: "update_self"},
	})

	// 访客角色，只有读取权限
	p.authorizer.AddRole("guest", []Permission{
		{Resource: "public", Action: "read"},
	})

	return p
}

// AddCustomRole 添加自定义角色
func (p *SimpleRBACProvider) AddCustomRole(name string, permissions []Permission, parents ...string) *SimpleRBACProvider {
	p.authorizer.AddRole(name, permissions, parents...)
	return p
}

// Build 构建授权器
func (p *SimpleRBACProvider) Build() *RBACAuthorizer {
	return p.authorizer
}
