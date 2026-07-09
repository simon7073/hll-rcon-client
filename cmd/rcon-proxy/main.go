// rcon-proxy — HLL RCON HTTP 代理服务
//
// 将 RCON 协议封装为 HTTP API，供非 Go 项目（如 Python）调用。
//
// API：
//
//	POST /servers                 注册服务器 {id, host, port, password}
//	DELETE /servers/:id           移除服务器
//	POST /servers/:id/command     执行命令 {command, params}
//	GET  /health                  健康检查
//	GET  /metrics                 连接池指标
//
// 启动：
//
//	rcon-proxy -port 8080
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/simon7073/hll-rcon-client/core"
	"github.com/simon7073/hll-rcon-client/rcon"
)

var (
	listenAddr = flag.String("port", "8080", "监听端口")
	rconProxy   = flag.String("rcon-http-proxy", "", "HTTP CONNECT proxy for RCON connections (仅在显式指定时生效，不读取系统环境变量)")
)

// ServerRegistration 服务器注册请求
type ServerRegistration struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Password string `json:"password"`
}

// CommandRequest 命令执行请求
type CommandRequest struct {
	Command string `json:"command"`
	Params  string `json:"params"`
}

// ServerEntry 内部服务器条目
type ServerEntry struct {
	Registration ServerRegistration
	ServerID     uint
}

type Proxy struct {
	pool      *rcon.Pool
	servers   map[string]*ServerEntry // id -> entry
	idCounter uint
}

func main() {
	flag.Parse()
	addr := ":" + *listenAddr

	proxy := &Proxy{
		pool:    rcon.NewPool(rcon.DefaultPoolConfig()),
		servers: make(map[string]*ServerEntry),
	}
	defer proxy.pool.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", proxy.handleHealth)
	mux.HandleFunc("/metrics", proxy.handleMetrics)
	mux.HandleFunc("POST /servers", proxy.registerServer)
	mux.HandleFunc("DELETE /servers/{id}", proxy.removeServer)
	mux.HandleFunc("POST /servers/{id}/command", proxy.handleServerCommand)

	server := &http.Server{
		Addr:         addr,
		Handler:      withLogging(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second, // RCON 可能较慢
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("[rcon-proxy] listening on %s", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"status":          "ok",
		"active_servers":  p.pool.ActiveServers(),
		"max_waiting":     p.pool.MaxWaitingCount(),
	})
}

func (p *Proxy) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"current": p.pool.MetricsNow(),
		"history": p.pool.MetricsHistory(),
	})
}

func (p *Proxy) registerServer(w http.ResponseWriter, r *http.Request) {
	var reg ServerRegistration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return
	}
	if reg.ID == "" || reg.Host == "" || reg.Password == "" {
		writeJSON(w, 400, map[string]string{"error": "id, host, password are required"})
		return
	}
	if reg.Port == "" {
		reg.Port = "29017"
	}

	if _, exists := p.servers[reg.ID]; exists {
		writeJSON(w, 409, map[string]string{"error": "server already registered"})
		return
	}

	p.idCounter++
	entry := &ServerEntry{
		Registration: reg,
		ServerID:     p.idCounter,
	}
	p.servers[reg.ID] = entry

	log.Printf("[rcon-proxy] server registered: id=%s, addr=%s:%s", reg.ID, reg.Host, reg.Port)
	writeJSON(w, 201, map[string]interface{}{
		"status":    "registered",
		"server_id": reg.ID,
	})
}

func (p *Proxy) removeServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	entry, exists := p.servers[id]
	if !exists {
		writeJSON(w, 404, map[string]string{"error": "server not found"})
		return
	}

	p.pool.RemoveServer(entry.ServerID)
	delete(p.servers, id)
	log.Printf("[rcon-proxy] server removed: id=%s", id)
	writeJSON(w, 200, map[string]string{"status": "removed"})
}

func (p *Proxy) handleServerCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	entry, exists := p.servers[id]
	if !exists {
		writeJSON(w, 404, map[string]string{"error": "server not found"})
		return
	}

	var req CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Command == "" {
		writeJSON(w, 400, map[string]string{"error": "command is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var opts []core.DialOption
	if *rconProxy != "" {
		opts = append(opts, core.WithHTTPProxy(*rconProxy))
	}

	client, err := p.pool.Acquire(ctx, entry.ServerID,
		entry.Registration.Host,
		entry.Registration.Port,
		entry.Registration.Password,
		opts...,
	)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": fmt.Sprintf("acquire connection: %v", err)})
		return
	}
	defer p.pool.Release(entry.ServerID, client)

	resp, err := client.Send(req.Command, req.Params, 30*time.Second)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": fmt.Sprintf("command error: %v", err)})
		return
	}

	writeJSON(w, 200, map[string]interface{}{
		"command": req.Command,
		"status":  resp.StatusCode,
		"message": resp.StatusMessage,
		"body":    resp.ContentBody,
	})
}


func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("[rcon-proxy] %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
		log.Printf("[rcon-proxy] %s %s took %v", r.Method, r.URL.Path, time.Since(start))
	})
}
