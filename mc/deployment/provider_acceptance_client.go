package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

const residentAcceptanceResponseLimit = 64 * 1024
const sha256DigestLength = 64

func validSHA256(value string) bool {
	if len(value) != sha256DigestLength {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func residentAcceptanceExchange(home string, request map[string]any) ([]byte, error) {
	return residentAcceptanceExchangeContext(context.Background(), home, request)
}

func residentAcceptanceExchangeContext(ctx context.Context, home string, request map[string]any) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(home, "health", "provider-acceptance.sock")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect resident acceptance socket: %w", err)
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 ||
		links != 1 {
		return nil, fmt.Errorf("resident acceptance socket is not owner-only")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("connect resident acceptance socket: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := conn.SetDeadline(time.Now().Add(45 * time.Second)); err != nil {
		return nil, err
	}
	if _, err := conn.Write(body); err != nil {
		return nil, fmt.Errorf("send resident acceptance request: %w", err)
	}
	response, err := io.ReadAll(io.LimitReader(conn, residentAcceptanceResponseLimit+1))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil || len(response) > residentAcceptanceResponseLimit {
		return nil, fmt.Errorf("read resident acceptance response: invalid bounded content")
	}
	return response, nil
}

func residentAcceptanceNonce() (string, error) {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonceBytes), nil
}
