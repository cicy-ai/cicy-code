package mitm

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
)

// passthrough copies bytes bidirectionally between the client and the target
// host without any decryption. Used when the host is not in the MITM
// whitelist, or when MITM termination fails (cert pinning) and we want to
// fall back transparently.
//
// dialUpstream is provided by the caller so we can route through mihomo /
// chain modes consistently with the MITM path.
func passthrough(ctx context.Context, client net.Conn, hostPort string, dialUpstream func(context.Context, string) (net.Conn, error)) error {
	upstream, err := dialUpstream(ctx, hostPort)
	if err != nil {
		return fmt.Errorf("mitm passthrough: dial %s: %w", hostPort, err)
	}
	defer upstream.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	errCh := make(chan error, 2)

	copy := func(dst, src net.Conn) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if err != nil && err != io.EOF {
			errCh <- err
		}
		// Half-close so the other direction can drain and exit.
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}

	go copy(upstream, client)
	go copy(client, upstream)
	wg.Wait()
	close(errCh)
	for e := range errCh {
		if e != nil {
			return e
		}
	}
	return nil
}
