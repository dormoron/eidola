package observability

import "net"

// GetOutboundIP obtains the preferred outbound IP address of this machine.
func GetOutboundIP() string {
	// Dial creates a connection to the address on the named network.
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		// If there was an error in establishing a connection, return an empty string.
		return ""
	}
	// Close the connection once this function is done executing.
	defer conn.Close()

	// LocalAddr returns the local network address.
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	// IP.String converts the IP address to a string.
	return localAddr.IP.String()
}
