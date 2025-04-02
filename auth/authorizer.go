package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrRoleNotFound       = errors.New("角色未找到")
	ErrPermissionNotFound = errors.New("权限未找到")
)

// RBACAuthorizer 基于角色的访问控制授权器
type RBACAuthorizer struct {
	// 角色到权限的映射
	rolePermissions map[string]map[string]bool
	// 资源动作到权限的映射
	resourceActionPermissions map[string]map[string]string
	// 互斥锁
	mu sync.RWMutex
}

// NewRBACAuthorizer 创建新的RBAC授权器
func NewRBACAuthorizer() *RBACAuthorizer {
	return &RBACAuthorizer{
		rolePermissions:           make(map[string]map[string]bool),
		resourceActionPermissions: make(map[string]map[string]string),
	}
}

// AddRole 添加角色
func (a *RBACAuthorizer) AddRole(role string, permissions []string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 创建权限映射
	perms := make(map[string]bool)
	for _, p := range permissions {
		perms[p] = true
	}

	a.rolePermissions[role] = perms
}

// RemoveRole 删除角色
func (a *RBACAuthorizer) RemoveRole(role string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.rolePermissions, role)
}

// AddPermissionToRole 向角色添加权限
func (a *RBACAuthorizer) AddPermissionToRole(role string, permission string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 检查角色是否存在
	perms, ok := a.rolePermissions[role]
	if !ok {
		return ErrRoleNotFound
	}

	// 添加权限
	perms[permission] = true

	return nil
}

// RemovePermissionFromRole 从角色中移除权限
func (a *RBACAuthorizer) RemovePermissionFromRole(role string, permission string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 检查角色是否存在
	perms, ok := a.rolePermissions[role]
	if !ok {
		return ErrRoleNotFound
	}

	// 移除权限
	delete(perms, permission)

	return nil
}

// MapResourceAction 将资源和动作映射到权限
func (a *RBACAuthorizer) MapResourceAction(resource, action, permission string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 获取资源映射
	actionMap, ok := a.resourceActionPermissions[resource]
	if !ok {
		actionMap = make(map[string]string)
		a.resourceActionPermissions[resource] = actionMap
	}

	// 映射动作到权限
	actionMap[action] = permission
}

// CheckPermission 检查用户是否有特定权限
func (a *RBACAuthorizer) CheckPermission(ctx context.Context, user *User, resource string, action string) (bool, error) {
	if user == nil {
		return false, errors.New("用户不能为空")
	}

	// 将资源和动作转换为权限标识
	permission, err := a.getPermissionForResourceAction(resource, action)
	if err != nil {
		return false, err
	}

	// 直接检查用户权限
	for _, p := range user.Permissions {
		if p == permission || p == "*" {
			return true, nil
		}
	}

	// 检查用户角色对应的权限
	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, role := range user.Roles {
		// 检查角色是否存在
		perms, ok := a.rolePermissions[role]
		if !ok {
			continue
		}

		// 检查角色是否有所需权限或全局权限
		if perms[permission] || perms["*"] {
			return true, nil
		}
	}

	return false, nil
}

// getPermissionForResourceAction 获取资源和动作对应的权限
func (a *RBACAuthorizer) getPermissionForResourceAction(resource, action string) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// 获取资源映射
	actionMap, ok := a.resourceActionPermissions[resource]
	if !ok {
		// 如果没有映射，使用标准格式
		return fmt.Sprintf("%s:%s", resource, action), nil
	}

	// 获取动作映射
	permission, ok := actionMap[action]
	if !ok {
		// 如果没有映射，使用标准格式
		return fmt.Sprintf("%s:%s", resource, action), nil
	}

	return permission, nil
}

// CasbinAuthorizer 使用Casbin的授权器
// 这里只提供框架代码，实际实现需要导入casbin依赖
type CasbinAuthorizer struct {
	// enforcer 是Casbin的执行器
	// enforcer *casbin.Enforcer
}

// NewCasbinAuthorizer 创建Casbin授权器
/*
func NewCasbinAuthorizer(modelPath, policyPath string) (*CasbinAuthorizer, error) {
	enforcer, err := casbin.NewEnforcer(modelPath, policyPath)
	if err != nil {
		return nil, err
	}

	return &CasbinAuthorizer{
		enforcer: enforcer,
	}, nil
}
*/

// CheckPermission 检查用户是否有特定权限
/*
func (a *CasbinAuthorizer) CheckPermission(ctx context.Context, user *User, resource string, action string) (bool, error) {
	if user == nil {
		return false, errors.New("用户不能为空")
	}

	// 使用Casbin检查权限
	allowed, err := a.enforcer.Enforce(user.ID, resource, action)
	if err != nil {
		return false, err
	}

	return allowed, nil
}
*/
