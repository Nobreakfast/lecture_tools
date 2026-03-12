package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/skip2/go-qrcode"
)

func main() {
	rawURL := flag.String("url", "", "要编码的网址")
	output := flag.String("out", "qrcode.png", "输出图片路径")
	size := flag.Int("size", 256, "二维码像素尺寸")
	flag.Parse()

	if err := run(strings.TrimSpace(*rawURL), strings.TrimSpace(*output), *size); err != nil {
		fmt.Fprintf(os.Stderr, "生成失败: %v\n", err)
		os.Exit(1)
	}
}

func run(rawURL, output string, size int) error {
	if rawURL == "" {
		return fmt.Errorf("请使用 -url 提供网址")
	}
	u, err := url.ParseRequestURI(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("网址格式无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("仅支持 http 或 https")
	}
	if output == "" {
		return fmt.Errorf("输出路径不能为空")
	}
	if size < 64 || size > 2048 {
		return fmt.Errorf("size 必须在 64-2048 之间")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	if err := qrcode.WriteFile(rawURL, qrcode.Medium, size, output); err != nil {
		return err
	}
	fmt.Printf("已生成二维码: %s\n", output)
	return nil
}
