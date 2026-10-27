package platform

import "net"

func parseIP(s string) net.IP { return net.ParseIP(s) }
