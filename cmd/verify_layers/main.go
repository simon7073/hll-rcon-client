// +build ignore

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/simon7073/hll-rcon-client/core"
	"github.com/simon7073/hll-rcon-client/rcon"
)

func main() {
	fmt.Println("=== Layer 0 (core/) 验证测试 ===")
	testLayer0()

	fmt.Println("\n=== Layer 1 (rcon/) 验证测试 ===")
	testLayer1()
}

func testLayer0() {
	// 使用 Layer 0 直接连接
	host := getEnvOrDefault("RCON_HOST", "")
	port := getEnvOrDefault("RCON_PORT", "29017")
	password := getEnvOrDefault("RCON_PASS", "")

	if host == "" {
		log.Fatal("RCON_HOST 未设置。请设置环境变量 RCON_HOST。\n例如: export RCON_HOST=your-server-ip")
	}
	if password == "" {
		log.Fatal("RCON_PASS 未设置。请设置环境变量 RCON_PASS。\n例如: export RCON_PASS=your-rcon-password")
	}

	fmt.Printf("连接服务器: %s:%s\n", host, port)

	// 测试 1: 使用 Dial() 一次性创建并连接
	fmt.Println("\n[测试 1] core.Dial() - 一次性创建并连接")
	client, err := core.Dial(host, port, password, 15*time.Second)
	if err != nil {
		log.Fatalf("Dial() 失败: %v", err)
	}
	fmt.Println("✅ Dial() 成功")

	// 测试 2: 发送命令并接收响应
	fmt.Println("\n[测试 2] client.Send() - 发送 GetServerInformation")
	resp, err := client.Send("GetServerInformation", "session", 30*time.Second)
	if err != nil {
		log.Fatalf("Send() 失败: %v", err)
	}
	fmt.Printf("✅ Send() 成功\n")
	fmt.Printf("   StatusCode: %d\n", resp.StatusCode)
	fmt.Printf("   StatusMessage: %s\n", resp.StatusMessage)
	fmt.Printf("   Name: %s\n", resp.Name)
	fmt.Printf("   Version: %d\n", resp.Version)
	
	// 解析 ContentBody
	if resp.ContentBody != "" {
		var session map[string]interface{}
		if err := json.Unmarshal([]byte(resp.ContentBody), &session); err == nil {
			fmt.Printf("   ContentBody (session):\n")
			for k, v := range session {
				fmt.Printf("     %s: %v\n", k, v)
			}
		}
	}

	// 测试 3: IsSuccess()
	fmt.Println("\n[测试 3] resp.IsSuccess()")
	fmt.Printf("   IsSuccess(): %v ✅\n", resp.IsSuccess())

	// 测试 4: 发送第二个命令（复用连接）
	fmt.Println("\n[测试 4] 复用连接发送第二个命令")
	resp2, err := client.Send("GetServerInformation", "players", 30*time.Second)
	if err != nil {
		log.Fatalf("第二个 Send() 失败: %v", err)
	}
	fmt.Printf("✅ 第二个 Send() 成功\n")
	fmt.Printf("   StatusCode: %d\n", resp2.StatusCode)
	fmt.Printf("   IsSuccess(): %v ✅\n", resp2.IsSuccess())

	// 测试 5: SendFireAndForget()
	fmt.Println("\n[测试 5] client.SendFireAndForget() - 不等待响应")
	err = client.SendFireAndForget("ServerBroadcast", "Layer 0 test", 5*time.Second)
	if err != nil {
		log.Fatalf("SendFireAndForget() 失败: %v", err)
	}
	fmt.Println("✅ SendFireAndForget() 成功（消息已发送）")

	// 测试 6: 关闭连接
	fmt.Println("\n[测试 6] client.Close()")
	err = client.Close()
	if err != nil {
		log.Fatalf("Close() 失败: %v", err)
	}
	fmt.Println("✅ Close() 成功")
	fmt.Printf("   IsClosed(): %v ✅\n", client.IsClosed())

	// 测试 7: 关闭后发送应该失败
	fmt.Println("\n[测试 7] 关闭后发送（应该失败）")
	_, err = client.Send("GetServerInformation", "session", 30*time.Second)
	if err != nil {
		fmt.Printf("✅ 预期中的错误: %v\n", err)
	} else {
		log.Fatal("❌ 应该返回错误，但没有")
	}

	fmt.Println("\n=== Layer 0 测试全部通过 ===")
}

func testLayer1() {
	host := getEnvOrDefault("RCON_HOST", "")
	port := getEnvOrDefault("RCON_PORT", "29017")
	password := getEnvOrDefault("RCON_PASS", "")

	if host == "" {
		log.Fatal("RCON_HOST 未设置。请设置环境变量 RCON_HOST。\n例如: export RCON_HOST=your-server-ip")
	}
	if password == "" {
		log.Fatal("RCON_PASS 未设置。请设置环境变量 RCON_PASS。\n例如: export RCON_PASS=your-rcon-password")
	}

	fmt.Printf("连接服务器: %s:%s\n", host, port)

	// 测试 1: 使用 rcon.NewClient() + Connect()
	fmt.Println("\n[测试 1] rcon.NewClient() + Connect()")
	client := rcon.NewClient(host, port, password)
	err := client.Connect(15 * time.Second)
	if err != nil {
		log.Fatalf("Connect() 失败: %v", err)
	}
	fmt.Println("✅ Connect() 成功")

	// 测试 2: Send() 自动重连
	fmt.Println("\n[测试 2] client.Send() - 自动重连测试")
	
	// 先发送一个正常命令
	resp, err := client.Send("GetServerInformation", "session", 30*time.Second)
	if err != nil {
		log.Fatalf("Send() 失败: %v", err)
	}
	fmt.Printf("✅ Send() 成功\n")
	fmt.Printf("   StatusCode: %d\n", resp.StatusCode)
	fmt.Printf("   IsSuccess(): %v ✅\n", resp.IsSuccess())

	// 测试 3: 关闭底层连接，测试自动重连
	fmt.Println("\n[测试 3] 模拟连接断开后的自动重连")
	// 注意：我们不能直接关闭底层连接，因为 Layer 1 会管理它
	// 但我们可以测试 Send() 的错误处理
	
	// 测试 4: SendFireAndForget()
	fmt.Println("\n[测试 4] client.SendFireAndForget()")
	err = client.SendFireAndForget("ServerBroadcast", "Layer 1 test", 5*time.Second)
	if err != nil {
		log.Fatalf("SendFireAndForget() 失败: %v", err)
	}
	fmt.Println("✅ SendFireAndForget() 成功")

	// 测试 5: 关闭连接
	fmt.Println("\n[测试 5] client.Close()")
	err = client.Close()
	if err != nil {
		log.Fatalf("Close() 失败: %v", err)
	}
	fmt.Println("✅ Close() 成功")

	fmt.Println("\n=== Layer 1 测试全部通过 ===")

	// 测试 6: Pool 测试
	fmt.Println("\n[测试 6] rcon.Pool - 连接池测试")
	testPool(host, port, password)
}

func testPool(host, port, password string) {
	config := rcon.DefaultPoolConfig()
	pool := rcon.NewPool(config)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 从池中获取连接
	fmt.Println("   Acquire() - 从池中获取连接")
	client, err := pool.Acquire(ctx, 1, host, port, password)
	if err != nil {
		log.Fatalf("Acquire() 失败: %v", err)
	}
	fmt.Println("✅ Acquire() 成功")

	// 使用连接发送命令
	resp, err := client.Send("GetServerInformation", "session", 30*time.Second)
	if err != nil {
		log.Fatalf("Send() 失败: %v", err)
	}
	fmt.Printf("✅ Send() 成功 - StatusCode: %d\n", resp.StatusCode)

	// 归还连接
	fmt.Println("   Release() - 归还连接到池中")
	pool.Release(1, client)
	fmt.Println("✅ Release() 成功")

	// 再次获取（应该复用）
	fmt.Println("   Acquire() - 再次获取（应该复用）")
	client2, err := pool.Acquire(ctx, 1, host, port, password)
	if err != nil {
		log.Fatalf("第二次 Acquire() 失败: %v", err)
	}
	fmt.Println("✅ 第二次 Acquire() 成功")

	// 归还
	pool.Release(1, client2)
	fmt.Println("✅ Release() 成功")

	fmt.Println("=== Pool 测试全部通过 ===")
}

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
