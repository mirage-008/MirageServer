package controller

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

func (c *Cockpit) BuildLinuxClient() {
	sysCfg := c.GetSysCfg()
	if sysCfg == nil || sysCfg.ClientVersion.Linux.Url == "" {
		return
	} // 未设置Linux客户端源码仓库，无需构建

	repoURL := sysCfg.ClientVersion.Linux.Url
	if sysCfg.ClientVersion.Linux.RepoCred != "" {
		repoURL = strings.Replace(repoURL, "://", "://"+sysCfg.ClientVersion.Linux.RepoCred+"@", 1)
	}
	var repoHash string
	if _, err := os.Stat("src/linux"); err != nil && !os.IsNotExist(err) {
		log.Error().Caller().Err(err).Msg("Linux 源码文件夹检查失败")
		c.markLinuxLastBuildFail()
		return
	} else if err == nil {
		// 获取本地Hash
		cmd := exec.Command("git", "ls-remote", "src/linux")
		var out bytes.Buffer
		cmd.Stdout = &out
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err = cmd.Run()
		if err != nil {
			log.Error().Caller().Msg("检查本地Linux源码仓库Hash出错" + stderr.String())
			c.markLinuxLastBuildFail()
			return
		}
		localHash, err := firstGitRemoteHash(out.String())
		if err != nil {
			log.Error().Caller().Err(err).Msg("解析本地Linux源码仓库Hash失败")
			c.markLinuxLastBuildFail()
			return
		}
		out.Reset()
		stderr.Reset()
		// 获取远程Hash
		cmd = exec.Command("git", "ls-remote", repoURL)
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		err = cmd.Run()
		if err != nil {
			log.Error().Caller().Msg("检查远程Linux源码仓库Hash出错" + stderr.String())
			c.markLinuxLastBuildFail()
			return
		}
		repoHash, err = firstGitRemoteHash(out.String())
		if err != nil {
			log.Error().Caller().Err(err).Msg("解析远程Linux源码仓库Hash失败")
			c.markLinuxLastBuildFail()
			return
		}

		if localHash != repoHash {
			c.markLinuxInBuilding(sysCfg) //进入构建状态
			err = os.RemoveAll("src/linux")
			if err != nil {
				log.Error().Caller().Err(err).Msg("Linux源码文件夹删除失败")
				c.markLinuxLastBuildFail()
				return
			}

			err = gitCloneRepo(repoURL, "src/linux")
			if err != nil {
				log.Warn().Caller().Err(err).Msg("Linux源码仓库更新失败")
				c.markLinuxLastBuildFail()
				return
			}
		} else if strings.Contains(sysCfg.ClientVersion.Linux.BuildState, "成功") {
			log.Info().Msg("已成功构建过同样Hash版本，无需重复构建")
			return
		} else {
			c.markLinuxInBuilding(sysCfg) //进入构建状态
		}
	} else if os.IsNotExist(err) {
		c.markLinuxInBuilding(sysCfg) //进入构建状态
		if _, err := os.Stat("src"); os.IsNotExist(err) {
			err = os.Mkdir("src", os.ModePerm)
			if err != nil {
				log.Error().Caller().Err(err).Msg("源码文件夹创建失败")
				c.markLinuxLastBuildFail()
				return
			}
		} else if err != nil {
			log.Error().Caller().Err(err).Msg("源码文件夹检查失败")
			c.markLinuxLastBuildFail()
			return
		}
		err = gitCloneRepo(repoURL, "src/linux")
		if err != nil {
			log.Error().Caller().Err(err).Msg("Linux源码仓库更新失败")
			c.markLinuxLastBuildFail()
			return
		}
	}
	// 以上保证需要构建的代码仓库已放到本地src/linux
	cmd := exec.Command("go", "run", "cmd/dist/dist.go", "build", "all")
	cmd.Dir = "src/linux"
	var out bytes.Buffer
	cmd.Stdout = &out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		log.Error().Caller().Msg("Linux客户端构建失败:" + stderr.String())
		c.markLinuxLastBuildFail()
		return
	}
	shortVersion, err := detectMirageDistVersion("src/linux/dist")
	if err != nil {
		log.Error().Caller().Err(err).Msg("Linux客户端构建成功，但读取构建版本失败")
		c.markLinuxLastBuildFail()
		return
	}
	log.Info().Msg("Linux客户端构建成功! 版本号：" + shortVersion)

	// 检查发布路径情况
	_, err = os.Stat("download")
	if err != nil {
		err = os.Mkdir("download", os.ModePerm)
		if err != nil {
			log.Error().Caller().Err(err).Msg("下载文件夹创建失败")
			c.markLinuxLastBuildFail()
			return
		}
	}

	if err = ReleaseDeb(sysCfg.ServerURL); err != nil {
		log.Error().Caller().Err(err).Msg("deb发布失败")
		c.markLinuxLastBuildFail()
		return
	}
	if err = ReleaseRpm(sysCfg.ServerURL); err != nil {
		log.Error().Caller().Err(err).Msg("rpm发布失败")
		c.markLinuxLastBuildFail()
		return
	}
	if err = ReleaseTgz(); err != nil {
		log.Error().Caller().Err(err).Msg("tgz发布失败")
		c.markLinuxLastBuildFail()
		return
	}

	// 记录构建成功，更新版本信息
	sysCfg = c.GetSysCfg()
	if sysCfg == nil {
		log.Error().Caller().Msg("构建完成，但获取系统配置失败")
		c.markLinuxLastBuildFail()
		return
	}
	sysCfg.ClientVersion.Linux.BuildState = "成功 " + Time2SHString(time.Now())
	sysCfg.ClientVersion.Linux.Version = shortVersion
	sysCfg.ClientVersion.Linux.Hash = repoHash
	if err := c.db.Model(&sysCfg).Update("client_version", sysCfg.ClientVersion).Error; err != nil {
		log.Error().Caller().Err(err).Msg("记录Linux客户端最近一次构建成功信息未能完成!")
	}
	log.Info().Msg("Linux客户端构建成功!")
	if c.serviceState {
		newCfg, err := c.GetSysCfg().toSrvConfig()
		if err != nil {
			log.Error().Caller().Err(err).Msg("Linux构建成功向控制器更新系统配置失败")
			return
		}
		c.CtrlChn <- CtrlMsg{
			Msg:    "update-config",
			SysCfg: newCfg,
		}
	}
}

func gitCloneRepo(repo, target string) error {
	cmd := exec.Command("git", "clone", "--recurse-submodules", repo, target)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return errors.New(stderr.String())
	}
	return nil
}

func ReleaseDeb(srvURL string) error {
	if err := os.MkdirAll("download/deb", os.ModePerm); err != nil {
		log.Error().Caller().Err(err).Msg("deb发布文件夹创建失败")
		return err
	}
	if err := moveDistArtifacts("src/linux/dist/*.deb", "download/deb"); err != nil {
		return err
	}

	var stderr bytes.Buffer
	cmd := exec.Command("sh", "-c", "dpkg-scanpackages deb /dev/null | gzip -9c > deb/Packages.gz")
	cmd.Dir = "download"
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return errors.New(stderr.String())
	}

	// 执行packages构建
	cmd = exec.Command("sh", "-c", "dpkg-scanpackages deb /dev/null | gzip -9c > deb/Packages.gz")
	cmd.Dir = "download"
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return errors.New(stderr.String())
	}

	// 创建list文件
	file, err := os.Create("download/deb/mirage.list")
	if err != nil {
		return err
	}
	defer file.Close()

	debList :=
		`# Mirage Repo for deb format
deb https://%s/download deb`
	_, err = fmt.Fprintf(file, debList, srvURL)
	if err != nil {
		return err
	}
	return nil
}

func ReleaseRpm(srvURL string) error {
	if err := os.MkdirAll("download/rpm", os.ModePerm); err != nil {
		log.Error().Caller().Err(err).Msg("rpm发布文件夹创建失败")
		return err
	}
	if err := moveDistArtifacts("src/linux/dist/*.rpm", "download/rpm"); err != nil {
		return err
	}
	var stderr bytes.Buffer

	// 执行packages构建 - 默认在ubuntu下执行，使用createrepo_c
	cmd := exec.Command("lsb_release", "-d")
	output, _ := cmd.CombinedOutput()

	if strings.Contains(string(output), "Ubuntu") {
		cmd = exec.Command("createrepo_c", "rpm", "--update")
	} else {
		cmd = exec.Command("createrepo", "rpm", "--update")
	}
	cmd.Dir = "download"
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.New(stderr.String())
	}

	// 创建repo文件
	file, err := os.Create("download/rpm/mirage.repo")
	if err != nil {
		return err
	}
	defer file.Close()

	rpmRepo :=
		`[mirage]
name=Mirage
baseurl=https://%s/download/rpm
enabled=1
type=rpm
repo_gpgcheck=0
gpgcheck=0`
	_, err = fmt.Fprintf(file, rpmRepo, srvURL)
	if err != nil {
		return err
	}
	return nil
}

func ReleaseTgz() error {
	if err := os.MkdirAll("download/tgz", os.ModePerm); err != nil {
		log.Error().Caller().Err(err).Msg("tgz发布文件夹创建失败")
		return err
	}
	if err := moveDistArtifacts("src/linux/dist/*.tgz", "download/tgz"); err != nil {
		return err
	}
	return nil
}

func firstGitRemoteHash(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			return fields[0], nil
		}
	}
	return "", errors.New("未找到git远程哈希")
}

func detectMirageDistVersion(distDir string) (string, error) {
	patterns := []string{"mirage_*.deb", "mirage_*.rpm", "mirage_*.tgz"}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(distDir, pattern))
		if err != nil {
			return "", err
		}
		if len(matches) == 0 {
			continue
		}
		return versionFromArtifactName(filepath.Base(matches[0]))
	}
	return "", errors.New("未找到Mirage构建产物")
}

func versionFromArtifactName(name string) (string, error) {
	if !strings.HasPrefix(name, "mirage_") {
		return "", fmt.Errorf("未知产物文件名: %s", name)
	}
	rest := strings.TrimPrefix(name, "mirage_")
	idx := strings.Index(rest, "_")
	if idx <= 0 {
		return "", fmt.Errorf("无法从产物文件名解析版本: %s", name)
	}
	return rest[:idx], nil
}

func moveDistArtifacts(pattern, dstDir string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("未找到构建产物: %s", pattern)
	}
	for _, src := range matches {
		dst := filepath.Join(dstDir, filepath.Base(src))
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cockpit) markLinuxInBuilding(sysCfg *SysConfig) {
	if sysCfg == nil {
		return
	}
	sysCfg.ClientVersion.Linux.BuildState = "正在进行 "
	if err := c.db.Model(&sysCfg).Update("client_version", sysCfg.ClientVersion).Error; err != nil {
		log.Error().Caller().Err(err).Msg("标记正在构建Linux客户端状态未能完成!")
		return
	}
}

func (c *Cockpit) markLinuxLastBuildFail() {
	sysCfg := c.GetSysCfg()
	if sysCfg == nil {
		return
	}
	sysCfg.ClientVersion.Linux.BuildState = "失败 " + Time2SHString(time.Now())
	if err := c.db.Model(&sysCfg).Update("client_version", sysCfg.ClientVersion).Error; err != nil {
		log.Error().Caller().Err(err).Msg("记录Linux客户端最近一次构建失败信息未能完成!")
		return
	}
}
