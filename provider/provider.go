package provider

import (
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/cheggaaa/pb.v1"

	"github.com/bogem/id3v2"

	"github.com/winterssy/music-get/pkg/ecode"

	"github.com/winterssy/easylog"
	"github.com/winterssy/music-get/conf"
	"github.com/winterssy/music-get/utils"
)

const (
	NetEaseMusic = iota
	QQMusic
)

type MusicRequest interface {
	// 是否需要登录
	RequireLogin() bool
	// 发起登录请求
	Login() error
	// 发起API请求
	Do() error
	// 解析API响应获取音源
	Prepare() ([]*MP3, error)
}

type MP3 struct {
	FileName    string
	SavePath    string
	Playable    bool
	DownloadURL string
	Tag         Tag
	Provider    int
}

type Tag struct {
	Title         string
	Artist        string
	Album         string
	Year          string
	Track         string
	CoverImageURL string
}

type DownloadTask struct {
	MP3    *MP3
	Status int
}

func (m *MP3) SingleDownload() (status int) {
	defer func() {
		switch status {
		case ecode.Success:
			easylog.Infof("Download complete")
		case ecode.NoCopyright, ecode.AlreadyDownloaded:
			easylog.Warnf("Download interrupt: %s", ecode.Message(status))
		default:
			easylog.Errorf("Download error: %s", ecode.Message(status))
		}
	}()

	if !m.Playable {
		status = ecode.NoCopyright
		return
	}

	m.SavePath = filepath.Join(conf.MP3DownloadDir, m.SavePath)
	if err := utils.BuildPathIfNotExist(m.SavePath); err != nil {
		status = ecode.BuildPathException
		return
	}

	fPath := filepath.Join(m.SavePath, m.FileName)
	if !conf.DownloadOverwrite {
		if downloaded, _ := utils.ExistsPath(fPath); downloaded {
			status = ecode.AlreadyDownloaded
			return
		}
	}

	easylog.Infof("Downloading: %s", m.FileName)
	resp, err := Request("GET", m.DownloadURL, nil, nil, m.Provider)
	if err != nil {
		status = ecode.HTTPRequestException
		return
	}
	defer resp.Body.Close()

	f, err := os.Create(fPath)
	if err != nil {
		status = ecode.BuildFileException
		return
	}
	defer f.Close()

	bar := pb.New(int(resp.ContentLength)).SetUnits(pb.U_BYTES).SetRefreshRate(100 * time.Millisecond)
	bar.ShowSpeed = true
	bar.Start()
	reader := bar.NewProxyReader(resp.Body)
	n, err := io.Copy(f, reader)
	if err != nil || n != resp.ContentLength {
		status = ecode.FileTransferException
		return
	}

	bar.Finish()
	status = ecode.Success
	return
}

func (m *MP3) ConcurrentDownload(taskList chan DownloadTask, taskQueue chan struct{}, wg *sync.WaitGroup) {
	var status int

	defer func() {
		switch status {
		case ecode.Success:
			easylog.Infof("Download complete: %s", m.FileName)
		case ecode.NoCopyright, ecode.AlreadyDownloaded:
			easylog.Warnf("Download interrupt: %s: %s", m.FileName, ecode.Message(status))
		default:
			easylog.Errorf("Download error: %s: %s", m.FileName, ecode.Message(status))
		}
		wg.Done()
		taskList <- DownloadTask{m, status}
		<-taskQueue
	}()

	if !m.Playable {
		status = ecode.NoCopyright
		return
	}

	m.SavePath = filepath.Join(conf.MP3DownloadDir, m.SavePath)
	if err := utils.BuildPathIfNotExist(m.SavePath); err != nil {
		status = ecode.BuildPathException
		return
	}

	fPath := filepath.Join(m.SavePath, m.FileName)
	if !conf.DownloadOverwrite {
		if downloaded, _ := utils.ExistsPath(fPath); downloaded {
			status = ecode.AlreadyDownloaded
			return
		}
	}

	easylog.Infof("Downloading: %s", m.FileName)
	resp, err := Request("GET", m.DownloadURL, nil, nil, m.Provider)
	if err != nil {
		status = ecode.HTTPRequestException
		return
	}
	defer resp.Body.Close()

	f, err := os.Create(fPath)
	if err != nil {
		status = ecode.BuildFileException
		return
	}
	defer f.Close()

	n, err := io.Copy(f, resp.Body)
	if err != nil || n != resp.ContentLength {
		status = ecode.BuildFileException
		return
	}

	status = ecode.Success
	return
}

func (m *MP3) UpdateTag(wg *sync.WaitGroup) {
	var err error
	defer func() {
		if err != nil {
			easylog.Errorf("Update music tag failure: %s: %s", m.FileName, err.Error())
		}
		wg.Done()
	}()

	file := filepath.Join(m.SavePath, m.FileName)
	tag, err := id3v2.Open(file, id3v2.Options{Parse: true})
	if err != nil {
		return
	}
	defer tag.Close()

	tag.SetDefaultEncoding(id3v2.EncodingUTF8)
	tag.SetTitle(m.Tag.Title)
	tag.SetArtist(m.Tag.Artist)
	tag.SetAlbum(m.Tag.Album)
	tag.SetYear(m.Tag.Year)
	textFrame := id3v2.TextFrame{
		Encoding: id3v2.EncodingUTF8,
		Text:     m.Tag.Track,
	}
	tag.AddFrame(tag.CommonID("Track number/Position in set"), textFrame)

	if picURL, _ := url.Parse(m.Tag.CoverImageURL); picURL != nil {
		if err = writeCoverImage(tag, m.Tag.CoverImageURL, m.Provider); err != nil {
			easylog.Errorf("Update music cover image failure: %s: %s", m.Tag.Title, err.Error())
		}
	}

	if err = tag.Save(); err == nil {
		easylog.Infof("Music tag updated: %s", m.FileName)
	}
}

func writeCoverImage(tag *id3v2.Tag, coverImage string, origin int) error {
	resp, err := Request("GET", coverImage, nil, nil, origin)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	pic := id3v2.PictureFrame{
		Encoding:    id3v2.EncodingUTF8,
		MimeType:    "image/jpg",
		PictureType: id3v2.PTFrontCover,
		Picture:     data,
	}
	tag.AddAttachedPicture(pic)
	return nil
}
