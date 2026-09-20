// Command server 启动粉末衍射晶格索引 HTTP 服务。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"pwidx/internal/server"
	"pwidx/internal/store"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5320", "HTTP 监听地址")
	dbPath := flag.String("db", "", "SQLite 数据库路径（默认 ./pwidx-state.db）")
	flag.Parse()

	if *dbPath == "" {
		*dbPath = filepath.Join(".", "pwidx-state.db")
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	if n, err := st.RecoverInterrupted(ctx); err != nil {
		log.Printf("恢复中断作业状态失败: %v", err)
	} else if n > 0 {
		log.Printf("已把 %d 个因进程退出而中断的作业标记为 aborted（未发布候选）", n)
	}

	srv, err := server.New(st)
	if err != nil {
		log.Fatalf("初始化服务失败: %v", err)
	}
	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("粉末衍射索引服务监听 http://%s", *listen)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务退出: %v", err)
		}
	}()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Printf("收到退出信号，正在关闭…")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
}
