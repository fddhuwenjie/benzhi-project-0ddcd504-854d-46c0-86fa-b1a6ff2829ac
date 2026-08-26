package main

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/httpapi"
	"archiveflow/internal/store"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19081", "监听地址")
	self := flag.Bool("self-check", false, "自检")
	flag.Parse()
	if *addr == "127.0.0.1:19081" {
		if p := os.Getenv("PORT"); p != "" {
			port, err := strconv.Atoi(p)
			if err != nil || port < 1 || port > 65535 {
				fmt.Fprintln(os.Stderr, "PORT 必须是有效端口号")
				os.Exit(2)
			}
			*addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		}
	}
	dir := os.TempDir() + "/archiveflow-data"
	s, err := store.New(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "存储初始化失败:", err)
		os.Exit(1)
	}
	a, err := audit.New(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "审计初始化失败:", err)
		os.Exit(1)
	}
	app := application.New(s, a)
	srv := &http.Server{Addr: *addr, Handler: httpapi.New(app).Routes()}
	if *self {
		listener, err := net.Listen("tcp", *addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "自检监听失败:", err)
			os.Exit(1)
		}
		go srv.Serve(listener)
		response, err := http.Get("http://" + listener.Addr().String() + "/healthz")
		if err != nil || response.StatusCode != http.StatusOK {
			fmt.Fprintln(os.Stderr, "自检请求失败")
			_ = srv.Close()
			os.Exit(1)
		}
		_ = response.Body.Close()
		fmt.Println("self-check ok")
		_ = srv.Close()
		return
	}
	fmt.Println("archiveflow listening", *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "服务退出:", err)
		os.Exit(1)
	}
}
