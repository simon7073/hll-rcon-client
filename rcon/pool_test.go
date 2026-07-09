package rcon

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/simon7073/hll-rcon-client/core"
)

// ==================== Pool Config ====================

func TestNewPool_ConfigClamping(t *testing.T) {
	// min > max → clamp min to max
	p := NewPool(PoolConfig{
		MinConnsPerServer: 20,
		MaxConnsPerServer: 5,
	})
	if p.config.MinConnsPerServer != 5 {
		t.Errorf("MinConnsPerServer should be clamped to MaxConnsPerServer, got %d", p.config.MinConnsPerServer)
	}

	// zero values → defaults
	p2 := NewPool(PoolConfig{})
	if p2.config.MinConnsPerServer != 2 {
		t.Errorf("zero MinConnsPerServer should default to 2, got %d", p2.config.MinConnsPerServer)
	}
	if p2.config.MaxConnsPerServer != 10 {
		t.Errorf("zero MaxConnsPerServer should default to 10, got %d", p2.config.MaxConnsPerServer)
	}
	if p2.config.ScaleUpWait != 500*time.Millisecond {
		t.Errorf("zero ScaleUpWait should default to 500ms, got %v", p2.config.ScaleUpWait)
	}
}

func TestPool_Close(t *testing.T) {
	p := NewPool(DefaultPoolConfig())
	// Close should drain WaitGroup and finish without hanging
	done := make(chan struct{})
	go func() {
		p.Close()
		close(done)
	}()
	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("Pool.Close() hung, likely WaitGroup leak")
	}
}

// ==================== Release nil/closed ====================

func TestPool_Release_NilClient(t *testing.T) {
	p := NewPool(DefaultPoolConfig())
	defer p.Close()
	// Release(nil) should not panic
	p.Release(1, nil)
}

func TestPool_Release_ClosedPool(t *testing.T) {
	p := NewPool(DefaultPoolConfig())
	p.Close()
	// Release to closed pool
	cc := core.NewClient("10.0.0.1", "12345")
	r := newClientWithCore("10.0.0.1", "12345", "pwd", cc)
	// should close the core client without panic
	p.Release(1, r)
	if !cc.IsClosed() {
		t.Error("core.Client should be closed when pool is closed")
	}
}

// ==================== Acquire type assertion ====================

func TestPool_Acquire_ReturnsRconClient(t *testing.T) {
	// 启动 mock RCON 服务器
	host, port, password, cleanup := startMockRCONServers(t, 1)
	defer cleanup()

	p := NewPool(PoolConfig{
		MinConnsPerServer: 1,
		MaxConnsPerServer: 3,
		ConnectTimeout:    5 * time.Second,
		ScaleUpWait:       100 * time.Millisecond,
		HealthInterval:    10 * time.Minute,
		DisableSSRFCheck:  true, // mock 服务器在 127.0.0.1
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := p.Acquire(ctx, 1, host, port, password)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	// 关键断言：返回类型必须是 *rcon.Client，不是 *core.Client
	if _, ok := interface{}(client).(*Client); !ok {
		t.Fatalf("Acquire returned %T, expected *rcon.Client", client)
	}
	if client.IsClosed() {
		t.Error("acquired client should not be closed")
	}

	// Release 后应正常归还
	p.Release(1, client)

	p.Close()
}

func TestPool_Acquire_ContextCancelled(t *testing.T) {
	p := NewPool(PoolConfig{
		MinConnsPerServer: 0,
		MaxConnsPerServer: 1,
		ScaleUpWait:       5 * time.Second,
	})
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	_, err := p.Acquire(ctx, 99, "10.0.0.1", "12345", "pwd")
	if err == nil {
		t.Error("cancelled context should return error")
	}
}

// ==================== Release CAS scale-down ====================

func TestPool_Release_ConcurrentScaleDown(t *testing.T) {
	host, port, password, cleanup := startMockRCONServers(t, 2)
	defer cleanup()

	p := NewPool(PoolConfig{
		MinConnsPerServer: 1,
		MaxConnsPerServer: 5,
		ConnectTimeout:    5 * time.Second,
		ScaleUpWait:       100 * time.Millisecond,
		HealthInterval:    10 * time.Minute,
		DisableSSRFCheck:  true, // mock 服务器在 127.0.0.1
	})

	ctx := context.Background()
	serverID := uint(2)

	// 获取 2 个连接
	c1, err := p.Acquire(ctx, serverID, host, port, password)
	if err != nil {
		t.Fatalf("Acquire c1: %v", err)
	}
	c2, err := p.Acquire(ctx, serverID, host, port, password)
	if err != nil {
		t.Fatalf("Acquire c2: %v", err)
	}

	// 并发归还 —— 验证 CAS 缩容不会 panic 或过度缩容
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		p.Release(serverID, c1)
	}()
	go func() {
		defer wg.Done()
		p.Release(serverID, c2)
	}()
	wg.Wait()

	// 验证池没有崩溃
	p.mu.RLock()
	_, ok := p.serverPools[serverID]
	p.mu.RUnlock()
	if !ok {
		t.Fatal("serverPool should still exist")
	}

	p.Close()
}

// ==================== Mock RCON Server ====================

// mockServer 模拟 HLL RCONv2 服务器，完成最小握手流程
type mockServer struct {
	ln       net.Listener
	password string
	key      []byte
	authCode int // 200=success, 401=unauthorized
	wg       sync.WaitGroup
}

func (ms *mockServer) serve(t *testing.T) {
	t.Helper()
	ms.wg.Add(1)
	go func() {
		defer ms.wg.Done()
		for {
			conn, err := ms.ln.Accept()
			if err != nil {
				return
			}
			go ms.handleConn(t, conn)
		}
	}()
}

func (ms *mockServer) handleConn(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()

	// Step 1: ServerConnect (unencrypted)
	var hdr core.Header
	if err := binary.Read(conn, binary.LittleEndian, &hdr); err != nil {
		return
	}
	body := make([]byte, hdr.Length)
	if _, err := io.ReadFull(conn, body); err != nil {
		return
	}
	keyBase64 := base64.StdEncoding.EncodeToString(ms.key)
	ms.sendResponse(t, conn, hdr.RequestID, 200, "OK", 2, "ServerConnect", keyBase64, nil)

	// Step 2: Login (XOR encrypted)
	if err := binary.Read(conn, binary.LittleEndian, &hdr); err != nil {
		return
	}
	encBody := make([]byte, hdr.Length)
	if _, err := io.ReadFull(conn, encBody); err != nil {
		return
	}
	if ms.authCode != 200 {
		ms.sendResponse(t, conn, hdr.RequestID, ms.authCode, "Not authenticated", 2, "Login", "", ms.key)
		return
	}
	ms.sendResponse(t, conn, hdr.RequestID, 200, "OK", 2, "Login", "mock-auth-token", ms.key)
}

func (ms *mockServer) sendResponse(t *testing.T, conn net.Conn, reqID uint32, code int, msg string, ver int, name string, content string, key []byte) {
	t.Helper()
	resp := map[string]interface{}{
		"StatusCode":    code,
		"StatusMessage": msg,
		"Version":       ver,
		"Name":          name,
		"ContentBody":   content,
	}
	body, _ := json.Marshal(resp)
	payload := make([]byte, len(body))
	copy(payload, body)
	if key != nil {
		for i := range payload {
			payload[i] = body[i] ^ key[i%len(key)]
		}
	}
	respHdr := core.Header{
		Magic:     core.MagicNumber,
		RequestID: reqID,
		Length:    uint32(len(payload)),
	}
	binary.Write(conn, binary.LittleEndian, &respHdr) //nolint:errcheck
	conn.Write(payload)                                //nolint:errcheck
}

func (ms *mockServer) close() {
	ms.ln.Close()
	ms.wg.Wait()
}

// startMockRCONServers 启动 N 个 mock RCON 服务器，返回最后一个的 host/port/password
func startMockRCONServers(t *testing.T, count int) (host, port, password string, cleanup func()) {
	t.Helper()
	var servers []*mockServer
	cleanup = func() {
		for _, ms := range servers {
			ms.close()
		}
	}
	for i := 0; i < count; i++ {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("mock server listen: %v", err)
		}
		ms := &mockServer{
			ln:       ln,
			password: "test-password",
			key:      []byte{0x01, 0x02, 0x03, 0x04},
			authCode: 200,
		}
		ms.serve(t)
		servers = append(servers, ms)
		host, port, password = "127.0.0.1", "0", "test-password"
		if i == count-1 {
			_, portStr, _ := net.SplitHostPort(ln.Addr().String())
			host = "127.0.0.1"
			port = portStr
		}
	}
	return
}
