package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/winterssy/music-get/pkg/ecode"
	"github.com/winterssy/music-get/provider"
)

type DownloadError struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Reason string `json:"reason"`
}

func SingleDownload(mp3List []*provider.MP3) {
	total, success, failure, ignore := len(mp3List), 0, 0, 0

	var failureInfo []DownloadError
	for _, m := range mp3List {
		switch status := m.SingleDownload(); status {
		case ecode.Success:
			success++
		case ecode.NoCopyright, ecode.AlreadyDownloaded:
			ignore++
		default:
			failure++
			failureInfo = append(failureInfo, DownloadError{m.FileName, m.DownloadURL, ecode.Message(status)})
			// ignore error
			os.Remove(filepath.Join(m.SavePath, m.FileName))
		}
	}

	fmt.Printf("\nDownload report --> total: %d, success: %d, failure: %d, ignore: %d\n", total, success, failure, ignore)

	if len(failureInfo) == 0 {
		return
	}
	if err := outputLog(failureInfo); err == nil {
		fmt.Printf("\nSee more info in %q\n", LogFileName)
	}
}

func ConcurrentDownload(mp3List []*provider.MP3, n int) {
	total, success, failure, ignore := len(mp3List), 0, 0, 0

	taskList := make(chan provider.DownloadTask, total)
	taskQueue := make(chan struct{}, n)
	wg := &sync.WaitGroup{}
	wg.Add(total)
	for _, i := range mp3List {
		taskQueue <- struct{}{}
		go i.ConcurrentDownload(taskList, taskQueue, wg)
	}
	wg.Wait()

	var failureInfo []DownloadError
	for range mp3List {
		task := <-taskList
		switch task.Status {
		case ecode.Success:
			success++
		case ecode.NoCopyright, ecode.AlreadyDownloaded:
			ignore++
		default:
			failure++
			failureInfo = append(failureInfo, DownloadError{task.MP3.FileName, task.MP3.DownloadURL, ecode.Message(task.Status)})
			// ignore error
			os.Remove(filepath.Join(task.MP3.SavePath, task.MP3.FileName))
		}
	}

	fmt.Printf("\nDownload report --> total: %d, success: %d, failure: %d, ignore: %d\n", total, success, failure, ignore)

	if len(failureInfo) == 0 {
		return
	}
	if err := outputLog(failureInfo); err == nil {
		fmt.Printf("\nSee more info in %q\n", LogFileName)
	}
}
