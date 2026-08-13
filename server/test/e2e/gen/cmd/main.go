// Command gen 生成基准参照 MP4 与 metadata.json。
//
// 用法：
//
//	go run ./test/e2e/gen/cmd -out test/fixtures [-scenario ref_1080p_h264_30fps_10s]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/x-media/x-media-server/test/e2e/gen"
)

func main() {
	out := flag.String("out", "", "输出目录（必填）")
	scenario := flag.String("scenario", "ref_1080p_h264_30fps_10s", "场景名，决定文件命名")
	noAudio := flag.Bool("no-audio", false, "不生成音频轨")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "-out 必填")
		os.Exit(2)
	}

	cfg := gen.Config{
		Scenario:  *scenario,
		Width:     1920,
		Height:    1080,
		FPS:       30,
		Frames:    300, // 10s
		WithAudio: !*noAudio,
		CRF:       23,
		GOP:       60,
		Bframes:   -1, // libx264 默认（含 B 帧）
	}
	cfg.FillDefaults()

	fmt.Printf("rendering %d frames %dx%d@%d ...\n", cfg.Frames, cfg.Width, cfg.Height, cfg.FPS)
	video := make([]byte, 0, cfg.Width*cfg.Height*3*cfg.Frames)
	for n := 1; n <= cfg.Frames; n++ {
		video = append(video, cfg.RenderFrame(n)...)
	}

	var audio []byte
	if cfg.WithAudio {
		fmt.Printf("rendering audio %d chunks ...\n", cfg.AudioChunks())
		audio = cfg.RenderAudio()
	}

	mp4 := filepath.Join(*out, *scenario+".mp4")
	meta := filepath.Join(*out, *scenario+".metadata.json")
	fmt.Printf("encoding -> %s\n", mp4)
	if err := cfg.Generate(video, audio, mp4, meta); err != nil {
		fmt.Fprintf(os.Stderr, "generate failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("done")
}
