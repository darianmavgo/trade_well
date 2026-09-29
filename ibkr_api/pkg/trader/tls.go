package trader

import "crypto/tls"

// insecureLoopbackTLS trusts the Client Portal Gateway's self-signed certificate. It is
// only ever applied when the gateway URL is on this machine (see NewTraderClient).
func insecureLoopbackTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} // #nosec G402: loopback gateway, self-signed by IBKR
}
