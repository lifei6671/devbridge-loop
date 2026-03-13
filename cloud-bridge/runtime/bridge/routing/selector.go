package routing

import "github.com/lifei6671/devbridge-loop/ltfp/pb"

// Selector 从候选路由中选出最终路由。
type Selector struct{}

// NewSelector 创建路由选择器。
func NewSelector() *Selector {
	return &Selector{}
}

// Select 返回排序后候选中的首个路由。
func (selector *Selector) Select(candidates []pb.Route) (pb.Route, bool) {
	_ = selector
	if len(candidates) == 0 {
		return pb.Route{}, false
	}
	return candidates[0], true
}
