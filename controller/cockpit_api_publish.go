package controller

import (
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"
)

const windowsLatestInstallerName = "MirageSetup-latest.exe"

func sanitizeUploadFilename(fileName string) string {
	fileName = strings.TrimSpace(strings.ReplaceAll(fileName, "\\", "/"))
	if fileName == "" {
		return ""
	}

	return path.Base(fileName)
}

func ensurePublishDir(dirPath string) error {
	return os.MkdirAll(dirPath, os.ModePerm)
}

func writePublishedFile(filePath string, fileData []byte) error {
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, fileData, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	return nil
}

func buildDownloadURL(serverURL, fileName string) string {
	return "https://" + serverURL + "/download/" + strings.ReplaceAll(fileName, "\\", "/")
}

// 接受/cockpit/api/publish的Post请求，用于进行客户端发布
// 根据请求类型不同，可能是json报文发送来的版本号和URL，也可能是form表单发送来的版本号和文件流
func (c *Cockpit) CAPIPublishClient(
	w http.ResponseWriter,
	r *http.Request,
) {
	vars := mux.Vars(r)
	osType, ok := vars["os"]
	if !ok {
		c.doAPIResponse(w, "未指定客户端类型", nil)
		return
	}

	sysCfg := c.GetSysCfg()
	if sysCfg == nil {
		c.doAPIResponse(w, "获取系统配置失败", nil)
		return
	}

	reqData := ClientVer{}

	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		json.NewDecoder(r.Body).Decode(&reqData)
	} else {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			c.doAPIResponse(w, "表单解析失败:"+err.Error(), nil)
			return
		}
		mForm := r.MultipartForm

		files, ok := mForm.File["file"]
		if !ok || len(files) == 0 {
			c.doAPIResponse(w, "未上传文件", nil)
			return
		}
		fileName := sanitizeUploadFilename(files[0].Filename)
		if fileName == "" {
			c.doAPIResponse(w, "文件名无效", nil)
			return
		}
		if strings.HasPrefix(osType, "navi") {
			fileName = "MirageNavi"
		}

		versions := mForm.Value["version"]
		if len(versions) > 0 {
			reqData.Version = versions[0]
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			c.doAPIResponse(w, "文件解析失败:"+err.Error(), nil)
			return
		}
		defer file.Close()
		fileData, err := io.ReadAll(file)
		if err != nil {
			c.doAPIResponse(w, "文件读取失败:"+err.Error(), nil)
			return
		}

		if err := ensurePublishDir("download"); err != nil {
			c.doAPIResponse(w, "下载文件夹创建失败:"+err.Error(), nil)
			return
		}

		switch osType {
		case "win":
			if !strings.HasSuffix(strings.ToLower(fileName), ".exe") {
				c.doAPIResponse(w, "Windows安装器必须是.exe文件", nil)
				return
			}
		case "navi_x86_64":
			if err := ensurePublishDir(filepath.Join("download", "x86_64")); err != nil {
				c.doAPIResponse(w, "下载文件夹创建失败:"+err.Error(), nil)
				return
			}
			fileName = filepath.ToSlash(filepath.Join("x86_64", fileName))
		case "navi_aarch64":
			if err := ensurePublishDir(filepath.Join("download", "aarch64")); err != nil {
				c.doAPIResponse(w, "下载文件夹创建失败:"+err.Error(), nil)
				return
			}
			fileName = filepath.ToSlash(filepath.Join("aarch64", fileName))
		}

		if err := writePublishedFile(filepath.Join("download", fileName), fileData); err != nil {
			c.doAPIResponse(w, "文件写入失败:"+err.Error(), nil)
			return
		}
		reqData.Url = buildDownloadURL(sysCfg.ServerURL, fileName)
		if osType == "win" {
			if err := writePublishedFile(filepath.Join("download", windowsLatestInstallerName), fileData); err != nil {
				c.doAPIResponse(w, "Windows安装器最新别名写入失败:"+err.Error(), nil)
				return
			}
			reqData.Url = buildDownloadURL(sysCfg.ServerURL, windowsLatestInstallerName)
		}
	}

	if reqData.Url == "" || reqData.Version == "" && osType != "linux" {
		c.doAPIResponse(w, "客户端发布请求处理失败", nil)
		return
	}

	switch osType {
	case "win":
		sysCfg.ClientVersion.Win = reqData
	case "ios_store":
		sysCfg.ClientVersion.IOSStore = reqData
	case "ios_test":
		sysCfg.ClientVersion.IOSTestFlight = reqData
	case "navi_x86_64":
		sysCfg.ClientVersion.NaviAMD64 = reqData.Version
	case "navi_aarch64":
		sysCfg.ClientVersion.NaviAARCH64 = reqData.Version
	case "linux":
		sysCfg.ClientVersion.Linux.Url = reqData.Url
		if reqData.Version != "" {
			sysCfg.ClientVersion.Linux.RepoCred = reqData.Version
			if reqData.Version == "clear" {
				sysCfg.ClientVersion.Linux.RepoCred = ""
			}
		}
	default:
		c.doAPIResponse(w, "未支持的客户端类型", nil)
		return
	}
	if err := c.db.Save(sysCfg).Error; err != nil {
		c.doAPIResponse(w, "更新客户端信息失败", nil)
		return
	}

	if osType == "linux" {
		go c.BuildLinuxClient()
	}

	if c.serviceState {
		newCfg, err := c.GetSysCfg().toSrvConfig()
		if err != nil {
			c.doAPIResponse(w, "更新系统配置失败", nil)
			return
		}
		c.CtrlChn <- CtrlMsg{
			Msg:    "update-config",
			SysCfg: newCfg,
		}
	}

	c.GetSettingGeneral(w, r)
}

type PublishInfoData struct {
	UploadURL     string            `json:"upload_url"`
	ClientVersion ClientVersionInfo `json:"client_version"`
}

func buildPublishUploadURL() string {
	// Uploads must stay on the same origin as cockpit so the existing
	// authenticated cookie is sent even when ServerURL differs from the
	// address the admin is currently using to access the panel.
	return "/cockpit/api/publish"
}

func (c *Cockpit) GetPublishInfo(
	w http.ResponseWriter,
	r *http.Request,
) {
	sysCfg := c.GetSysCfg()
	if sysCfg == nil {
		c.doAPIResponse(w, "获取系统配置失败", nil)
		return
	}
	c.doAPIResponse(w, "", PublishInfoData{
		UploadURL:     buildPublishUploadURL(),
		ClientVersion: sysCfg.ClientVersion,
	})
}
