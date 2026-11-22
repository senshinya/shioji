package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"

	"shioji/internal/app"
	"shioji/internal/mobileclient"
	"shioji/internal/runtimebuild"
	"shioji/internal/transport"
)

func (a *mobileApp) runWorkerSetup(w *mygo.Window, endpoint mobileclient.Endpoint, installer transport.WorkerArtifact, req *mobileRequest,
	done func(mobileclient.Endpoint, *transport.WorkerInfo)) context.CancelFunc {
	signer, err := a.identity()
	if err != nil {
		req.err = err.Error()
		w.Invalidate()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	route := a.routeDial()
	req.busy, req.err = true, ""
	w.Invalidate()
	go func() {
		defer cancel()
		verified, info, err := endpoint.Bootstrap(ctx, signer, route, installer)
		var preparation *app.ServicePreparation
		if err == nil && info.Legacy == nil && info.Migration == nil && info.Minor < app.AgentSetupWorkerMinor {
			preparation, err = planMobileServicePreparation(info)
		}
		mygo.RunOnMain(func() {
			if !a.pageCurrent(w) {
				return
			}
			req.busy = false
			switch {
			case err == nil && (info.Legacy != nil || info.Migration != nil):
				a.openLegacyMigration(w, verified, info)
			case err == nil && a.workerPreparations[verified.DeviceID].Preparation.ID != "":
				a.openServicePreparation(w, a.workerPreparations[verified.DeviceID])
			case err == nil && preparation != nil:
				a.openServicePreparation(w, mobileServicePreparation{Endpoint: verified, Preparation: *preparation})
			case err == nil:
				done(verified, &info)
			case installer == nil && endpoint.DeviceID == "" && errors.Is(err, transport.ErrWorkerMissing):
				done(endpoint, nil)
			default:
				req.err = err.Error()
			}
			w.Invalidate()
		})
	}()
	return cancel
}

func (a *mobileApp) openSSHCredentials(parent *mygo.Window, endpoint mobileclient.Endpoint, keyType string) {
	name := deviceHost(endpoint)
	if a.preview {
		name = a.deviceName()
	}
	keyType = strings.ToUpper(strings.TrimPrefix(keyType, "ssh-"))
	var w *mygo.Window
	var stop context.CancelFunc
	req := mobileRequest{}
	w = a.page("SSH 凭据", func(c *ui.Context) {
		mobilePageWithFooter(c, func() {
			p := palette(c)
			host, port, _ := net.SplitHostPort(endpoint.Address)
			address := host
			if host != name {
				address = name + " · " + host
			}
			mobilePageContext(c, address, endpoint.User, "端口 "+port)
			mobileSection(c, "主机身份")
			mobileDetailRow(c, keyType, "已核对主机公钥指纹", keyType, verifiedHostSymbol, nil,
				func() { label(c, "一致", captionTextSize, p.active) })
			mobileGroupHeading(c, "身份验证")
			mobileRow(c, "方式", "", "Shioji 专用密钥", nil, nil)
			node := "shioji-iphone"
			if isTablet() {
				node = "shioji-ipad"
			}
			mobileRow(c, "此设备的 SSH 公钥", node+" · ED25519", "", keySymbol, func() {
				if a.preview {
					return
				}
				key, err := a.identity()
				if err != nil {
					req.err = err.Error()
					return
				}
				mygo.Clipboard.WriteText(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey()))) + " Shioji-Mobile")
			})
			label(c, "在目标 Mac 的 SSH 帐户授权此公钥。私钥保存在此设备的 Keychain。", 13, p.secondary).Padding(8, 0)
			req.status(c)
		}, func() {
			primaryAction(c, "验证 SSH 登录", func() {
				if a.preview {
					a.openVerifiedWorker(w, endpoint, nil)
					return
				}
				stop = a.runWorkerSetup(w, endpoint, nil, &req, func(verified mobileclient.Endpoint, info *transport.WorkerInfo) {
					a.openVerifiedWorker(w, verified, info)
				})
			}).Disabled(req.busy)
		})
	})
	w.OnClosed(func() {
		if stop != nil {
			stop()
		}
	})
	a.pushNative(parent, w)
}

func workerSetupView(c *ui.Context, name, scope, state, detail string, req *mobileRequest, settings, footer func()) {
	mobilePageWithFooter(c, func() {
		mobilePageContext(c, name, scope)
		remoteStatus(c, serverSymbol, state, detail)
		if settings != nil {
			settings()
		}
		if req != nil {
			req.status(c)
		}
	}, footer)
}

func (a *mobileApp) openVerifiedWorker(parent *mygo.Window, endpoint mobileclient.Endpoint, worker *transport.WorkerInfo) {
	name := deviceHost(endpoint)
	if a.preview {
		name = a.deviceName()
	}
	var w *mygo.Window
	var stop context.CancelFunc
	req := mobileRequest{}
	w = a.page("执行服务", func(c *ui.Context) {
		state, detail := "尚未安装", "通过已验证的 SSH 连接安装已签名的执行服务。安装完成后即可配置远程 Agent。"
		if worker != nil {
			state = "执行服务已就绪"
			detail = fmt.Sprintf("协议 2.%d · 保留设备身份、工作区与会话。", worker.Minor)
		}
		workerSetupView(c, name, "SSH 身份已验证", state, detail, &req, nil, func() {
			if worker == nil {
				primaryAction(c, "安装执行服务", func() {
					if a.preview {
						worker = &transport.WorkerInfo{Minor: 50}
						w.Invalidate()
						return
					}
					stop = a.runWorkerSetup(w, endpoint, bundledWorkerArtifact, &req,
						func(verified mobileclient.Endpoint, info *transport.WorkerInfo) {
							endpoint, worker = verified, info
						})
				}).Disabled(req.busy)
				return
			}
			primaryAction(c, "配置远程 Agent", func() {
				if a.preview {
					a.openRemoteAgents(w)
					return
				}
				a.configureVerifiedRemoteAgent(w, endpoint, &req)
			})
		})
	})
	w.OnClosed(func() {
		if stop != nil {
			stop()
		}
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) configureVerifiedRemoteAgent(w *mygo.Window, endpoint mobileclient.Endpoint, req *mobileRequest) {
	if !a.pageCurrent(w) {
		return
	}
	if err := a.switchSavedDevice(endpoint); err != nil {
		req.err = err.Error()
		w.Invalidate()
		return
	}
	a.resetDeviceNavigation(func(err error) {
		if err == nil && a.endpoint.DeviceID == endpoint.DeviceID {
			a.openRemoteAgents(a.devices)
		}
	})
}

// These assets are part of the signed iOS bundle. The remote bootstrap checks
// the digest and the Mach-O code signature again before starting the service.
func bundledWorkerArtifact(_ context.Context) (io.ReadCloser, string, error) {
	path, err := bundledWorkerPath()
	if err != nil {
		return nil, "", err
	}
	file, digest, err := mobileclient.OpenWorkerArtifact(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", errors.New("此版本未包含安装包，请在目标 Mac 安装 Shioji 后重新验证")
	}
	return file, digest, err
}

func bundledWorkerPath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(executable), "worker-macos-arm64.data"), nil
}

func bundledWorkerRuntimeArtifact(_ context.Context) (io.ReadCloser, string, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(filepath.Dir(executable), "worker-runtime-macos-arm64.data")
	digest, err := runtimebuild.Digest(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", errors.New("此版本未包含签名后台宿主，请更新 Shioji")
	}
	if err != nil {
		return nil, "", err
	}
	file, err := os.Open(path)
	return file, digest, err
}
