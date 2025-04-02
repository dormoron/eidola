package authorization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// 权限相关的错误
var (
	ErrPermissionDenied  = errors.New("权限被拒绝")
	ErrRoleNotFound      = errors.New("角色不存在")
	ErrInvalidPermission = errors.New("无效的权限")
	ErrNoMetadata        = errors.New("上下文中没有元数据")
	ErrNoCredentials     = errors.New("上下文中没有认证信息")
)

// Permission 表示权限
type Permission string

// 预定义权限常量
const (
	PermissionCreate Permission = "create"
	PermissionRead   Permission = "read"
	PermissionUpdate Permission = "update"
	PermissionDelete Permission = "delete"
	PermissionAdmin  Permission = "admin"
)

// ParsePermission 从字符串解析权限
func ParsePermission(s string) (Permission, error) {
	switch strings.ToLower(s) {
	case string(PermissionCreate):
		return PermissionCreate, nil
	case string(PermissionRead):
		return PermissionRead, nil
	case string(PermissionUpdate):
		return PermissionUpdate, nil
	case string(PermissionDelete):
		return PermissionDelete, nil
	case string(PermissionAdmin):
		return PermissionAdmin, nil
	default:
		return "", ErrInvalidPermission
	}
}

// Resource 表示资源
type Resource string

// Role 表示角色
type Role struct {
	Name        string
	Description string
	// 资源-权限映射
	Permissions map[Resource][]Permission
}

// HasPermission 检查角色是否有对资源的特定权限
func (r *Role) HasPermission(resource Resource, permission Permission) bool {
	permissions, ok := r.Permissions[resource]
	if !ok {
		return false
	}

	// 检查角色是否拥有管理员权限
	for _, p := range permissions {
		if p == PermissionAdmin {
			return true
		}
	}

	// 检查角色是否拥有特定权限
	for _, p := range permissions {
		if p == permission {
			return true
		}
	}

	return false
}

// RBACAuthorizer 是基于角色的访问控制授权器
type RBACAuthorizer struct {
	roles       map[string]*Role
	permissions map[string][]Permission
	mu          sync.RWMutex
}

// NewRBACAuthorizer 创建新的RBAC授权器
func NewRBACAuthorizer() *RBACAuthorizer {
	return &RBACAuthorizer{
		roles:       make(map[string]*Role),
		permissions: make(map[string][]Permission),
	}
}

// AddRole 添加角色
func (a *RBACAuthorizer) AddRole(role *Role) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.roles[role.Name] = role
}

// RemoveRole 移除角色
func (a *RBACAuthorizer) RemoveRole(roleName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.roles, roleName)
}

// GetRole 获取角色
func (a *RBACAuthorizer) GetRole(roleName string) (*Role, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	role, ok := a.roles[roleName]
	if !ok {
		return nil, ErrRoleNotFound
	}
	return role, nil
}

// GetAllRoles 获取所有角色
func (a *RBACAuthorizer) GetAllRoles() []*Role {
	a.mu.RLock()
	defer a.mu.RUnlock()
	roles := make([]*Role, 0, len(a.roles))
	for _, role := range a.roles {
		roles = append(roles, role)
	}
	return roles
}

// MapMethodToPermission 映射gRPC方法到权限
func (a *RBACAuthorizer) MapMethodToPermission(method string, resource Resource, permission Permission) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 创建一个新的权限映射，形式为 "resource:permission"
	permStr := fmt.Sprintf("%s:%s", resource, permission)

	a.permissions[method] = append(a.permissions[method], Permission(permStr))
}

// MapServiceToResource 映射整个服务到资源权限
func (a *RBACAuthorizer) MapServiceToResource(serviceName string, resource Resource) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 映射服务名称到资源
	servicePattern := fmt.Sprintf("/%s/", serviceName)
	a.permissions[servicePattern] = append(a.permissions[servicePattern], Permission(string(resource)))
}

// getPermissionsForMethod 获取方法对应的权限
func (a *RBACAuthorizer) getPermissionsForMethod(method string) []Permission {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// 匹配完整方法名
	if perms, ok := a.permissions[method]; ok {
		return perms
	}

	// 尝试匹配服务名
	for pattern, perms := range a.permissions {
		if strings.HasSuffix(pattern, "/") && strings.HasPrefix(method, pattern) {
			return perms
		}
	}

	return nil
}

// CreatePermission 解析权限字符串
func (a *RBACAuthorizer) CreatePermission(resource Resource, permission Permission) Permission {
	return Permission(fmt.Sprintf("%s:%s", resource, permission))
}

// ParseResourcePermission 从权限字符串中解析资源和权限
func (a *RBACAuthorizer) ParseResourcePermission(perm Permission) (Resource, Permission, error) {
	parts := strings.SplitN(string(perm), ":", 2)
	if len(parts) != 2 {
		return "", "", ErrInvalidPermission
	}

	resource := Resource(parts[0])
	permission, err := ParsePermission(parts[1])
	if err != nil {
		return "", "", err
	}

	return resource, permission, nil
}

// UnaryServerInterceptor 创建用于一元RPC的授权拦截器
func (a *RBACAuthorizer) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// 从上下文获取角色信息
		role, err := a.getRoleFromContext(ctx)
		if err != nil {
			return nil, status.Errorf(codes.PermissionDenied, "授权失败: %v", err)
		}

		// 获取方法需要的权限
		requiredPerms := a.getPermissionsForMethod(info.FullMethod)
		if len(requiredPerms) == 0 {
			// 如果没有定义权限要求，则允许访问
			return handler(ctx, req)
		}

		// 检查权限
		for _, perm := range requiredPerms {
			resource, permission, err := a.ParseResourcePermission(perm)
			if err != nil {
				continue
			}

			// 如果角色有任何一个所需的权限，则允许访问
			if role.HasPermission(resource, permission) {
				return handler(ctx, req)
			}
		}

		// 没有任何所需的权限
		return nil, status.Errorf(codes.PermissionDenied, "权限被拒绝: 缺少所需权限")
	}
}

// StreamServerInterceptor 创建用于流RPC的授权拦截器
func (a *RBACAuthorizer) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// 从上下文获取角色信息
		role, err := a.getRoleFromContext(ss.Context())
		if err != nil {
			return status.Errorf(codes.PermissionDenied, "授权失败: %v", err)
		}

		// 获取方法需要的权限
		requiredPerms := a.getPermissionsForMethod(info.FullMethod)
		if len(requiredPerms) == 0 {
			// 如果没有定义权限要求，则允许访问
			return handler(srv, ss)
		}

		// 检查权限
		for _, perm := range requiredPerms {
			resource, permission, err := a.ParseResourcePermission(perm)
			if err != nil {
				continue
			}

			// 如果角色有任何一个所需的权限，则允许访问
			if role.HasPermission(resource, permission) {
				return handler(srv, ss)
			}
		}

		// 没有任何所需的权限
		return status.Errorf(codes.PermissionDenied, "权限被拒绝: 缺少所需权限")
	}
}

// getRoleFromContext 从上下文中获取角色
func (a *RBACAuthorizer) getRoleFromContext(ctx context.Context) (*Role, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, ErrNoMetadata
	}

	// 从元数据中获取角色
	roleValues := md.Get("role")
	if len(roleValues) == 0 {
		return nil, ErrNoCredentials
	}

	roleName := roleValues[0]
	role, err := a.GetRole(roleName)
	if err != nil {
		return nil, fmt.Errorf("获取角色失败: %w", err)
	}

	return role, nil
}

// AuthorizeByRole 根据角色授权
func (a *RBACAuthorizer) AuthorizeByRole(ctx context.Context, roleName string, resource Resource, permission Permission) error {
	role, err := a.GetRole(roleName)
	if err != nil {
		return err
	}

	if !role.HasPermission(resource, permission) {
		return ErrPermissionDenied
	}

	return nil
}

// SetupDefaultRoles 设置默认角色
func (a *RBACAuthorizer) SetupDefaultRoles() {
	// 创建管理员角色
	adminRole := &Role{
		Name:        "admin",
		Description: "系统管理员，拥有所有权限",
		Permissions: map[Resource][]Permission{
			"*": {PermissionAdmin}, // 全局管理员权限
		},
	}
	a.AddRole(adminRole)

	// 创建只读用户角色
	readOnlyRole := &Role{
		Name:        "readonly",
		Description: "只读用户，只能查看数据",
		Permissions: map[Resource][]Permission{
			"*": {PermissionRead}, // 全局只读权限
		},
	}
	a.AddRole(readOnlyRole)

	// 创建普通用户角色
	userRole := &Role{
		Name:        "user",
		Description: "普通用户，可以管理自己的数据",
		Permissions: map[Resource][]Permission{
			"user": {PermissionRead, PermissionUpdate},
			"data": {PermissionCreate, PermissionRead, PermissionUpdate, PermissionDelete},
		},
	}
	a.AddRole(userRole)
}

// AuthorizationMiddleware 授权中间件，用于检查特定权限
func AuthorizationMiddleware(authorizer *RBACAuthorizer, resource Resource, permission Permission) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		role, err := authorizer.getRoleFromContext(ctx)
		if err != nil {
			return nil, status.Errorf(codes.PermissionDenied, "授权失败: %v", err)
		}

		if !role.HasPermission(resource, permission) {
			return nil, status.Errorf(codes.PermissionDenied, "权限被拒绝: 需要 %s 资源的 %s 权限", resource, permission)
		}

		return handler(ctx, req)
	}
}

// NewRole 创建新角色
func NewRole(name, description string) *Role {
	return &Role{
		Name:        name,
		Description: description,
		Permissions: make(map[Resource][]Permission),
	}
}

// AddPermission 为角色添加权限
func (r *Role) AddPermission(resource Resource, permission Permission) {
	// 检查是否已经有此权限
	for _, p := range r.Permissions[resource] {
		if p == permission {
			return
		}
	}

	r.Permissions[resource] = append(r.Permissions[resource], permission)
}

// HasResource 检查角色是否可以访问资源
func (r *Role) HasResource(resource Resource) bool {
	// 检查通配符权限
	if _, ok := r.Permissions["*"]; ok {
		return true
	}

	_, ok := r.Permissions[resource]
	return ok
}

// RemovePermission 从角色移除权限
func (r *Role) RemovePermission(resource Resource, permission Permission) {
	permissions, ok := r.Permissions[resource]
	if !ok {
		return
	}

	// 过滤掉要删除的权限
	newPermissions := make([]Permission, 0, len(permissions))
	for _, p := range permissions {
		if p != permission {
			newPermissions = append(newPermissions, p)
		}
	}

	if len(newPermissions) == 0 {
		// 如果没有权限了，删除整个资源项
		delete(r.Permissions, resource)
	} else {
		r.Permissions[resource] = newPermissions
	}
}
