//go:build !windows

package netbridge

// Server 在非 Windows 平台为空实现；项目本身是 Windows GUI。
type Server struct{ URL string }

func Ensure(ProxyResolver) (*Server, error) { return nil, nil }
func Current() *Server                      { return nil }
func Stop()                                 {}
