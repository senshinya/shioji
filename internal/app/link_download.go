package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/portforward"
	"shioji/internal/protocol"
)

// FetchURL dials through the same authenticated owning worker as file links.
// In particular localhost and DNS refer to that device, never this client.
// No local listening port or long-lived port-forward mapping is created.
func (t AttachmentTarget) FetchURL(ctx context.Context, directory, raw string, progress func(domain.FileResource, int64)) (LinkedFile, error) {
	transport := &http.Transport{DialContext: func(call context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		number, err := strconv.Atoi(port)
		if err != nil {
			return nil, err
		}
		return protocol.OpenTCP(call, t.openStream, t.DeviceID, portforward.Target{Host: host, Port: number})
	}}
	defer transport.CloseIdleConnections()
	return downloadLinkHTTP(ctx, directory, raw, &http.Client{Transport: transport}, progress)
}

func downloadLinkHTTP(ctx context.Context, directory, raw string, client *http.Client, progress func(domain.FileResource, int64)) (result LinkedFile, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return result, errors.New("不支持的下载链接")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return result, err
	}
	response, err := client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("下载失败：HTTP %d", response.StatusCode)
	}
	name := filepath.Base(response.Request.URL.Path)
	if _, params, e := mime.ParseMediaType(response.Header.Get("Content-Disposition")); e == nil && params["filename"] != "" {
		name = filepath.Base(strings.ReplaceAll(params["filename"], "\\", "/"))
	}
	if name == "" || name == "." || name == "/" || name == ".." || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") {
		name = "download"
	}
	var identity [32]byte
	if _, err = rand.Read(identity[:]); err != nil {
		return result, err
	}
	// A unique, hash-shaped folder shares the existing disposable cache cleanup.
	linkedFileCacheMu.RLock()
	defer linkedFileCacheMu.RUnlock()
	folder := filepath.Join(directory, hex.EncodeToString(identity[:]))
	if err = os.MkdirAll(folder, 0700); err != nil {
		return result, err
	}
	complete := false
	defer func() {
		if !complete {
			os.RemoveAll(folder)
		}
	}()
	file, err := os.OpenFile(filepath.Join(folder, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	defer file.Close()
	meta := domain.FileResource{Name: name, MIME: response.Header.Get("Content-Type"), Size: response.ContentLength}
	hash := sha256.New()
	var done int64
	buffer := make([]byte, 64<<10)
	for {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		count, e := response.Body.Read(buffer)
		if count > 0 {
			if _, err = file.Write(buffer[:count]); err != nil {
				return result, err
			}
			hash.Write(buffer[:count])
			done += int64(count)
			if progress != nil {
				progress(meta, done)
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return result, e
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = file.Sync(); err != nil {
		return result, err
	}
	if err = file.Close(); err != nil {
		return result, err
	}
	meta.Size = done
	complete = true
	return LinkedFile{Path: file.Name(), Resource: meta, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}
