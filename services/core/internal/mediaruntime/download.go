package mediaruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"review-studio.local/core/internal/delivery"
)

func download(ctx context.Context, m Manifest, destination string, transport http.RoundTripper, progress func(int64, int64)) error {
	if err := payloadURL(m.URL, false); err != nil {
		return err
	}
	ctx, timeoutCancel := context.WithTimeout(ctx, 15*time.Minute)
	defer timeoutCancel()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if transport == nil {
		transport = &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second}).DialContext, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, DisableCompression: true}
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return bad("too many runtime redirects")
		}
		return payloadURL(req.URL.String(), true)
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return delivery.NewError(delivery.CodeNetworkFailure, "runtime download connection failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return delivery.NewError(delivery.CodeNetworkFailure, "runtime download HTTP status is not 200")
	}
	if response.ContentLength >= 0 && response.ContentLength != m.Size {
		return bad("runtime Content-Length mismatch")
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	good := false
	defer func() {
		f.Close()
		if !good {
			os.Remove(destination)
		}
	}()
	h := sha256.New()
	writer := io.MultiWriter(f, h)
	buf := make([]byte, 64<<10)
	var total int64
	// Body-read inactivity is bounded separately from total download time.
	idle := time.AfterFunc(30*time.Second, func() { cancel(context.DeadlineExceeded) })
	defer idle.Stop()
	for {
		n, e := response.Body.Read(buf)
		if n > 0 {
			idle.Reset(30 * time.Second)
			total += int64(n)
			if total > m.Size {
				return bad("runtime download exceeds signed size")
			}
			if _, err = writer.Write(buf[:n]); err != nil {
				return err
			}
			if progress != nil {
				progress(total, m.Size)
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return delivery.NewError(delivery.CodeNetworkFailure, "runtime download interrupted")
		}
	}
	if total != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return bad("runtime download size or SHA-256 mismatch")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	good = true
	return nil
}
