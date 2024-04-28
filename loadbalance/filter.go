package loadbalance

import (
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/resolver"
)

// Filter is a function type that takes balancer.PickInfo and resolver.Address
// and returns a bool indicating whether the address should be included.
type Filter func(info balancer.PickInfo, addr resolver.Address) bool

// GroupFilterBuilder constructs a Filter based on group attributes.
type GroupFilterBuilder struct{}

// Build returns a Filter function that filters resolver.Addresses
// based on the 'group' attribute in the context of balancer.PickInfo.
func (g GroupFilterBuilder) Build() Filter {
	return func(info balancer.PickInfo, addr resolver.Address) bool {
		// Retrieve the 'group' value from the address attributes.
		target, _ := addr.Attributes.Value("group").(string)
		// Retrieve the 'group' value from the PickInfo context.
		input, _ := info.Ctx.Value("group").(string)
		// Compare the groups to determine if the address should be included.
		return target == input
	}
}
