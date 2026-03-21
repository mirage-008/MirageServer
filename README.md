<img src="https://user-images.githubusercontent.com/7601383/219330724-55bc04e5-3f19-433b-98fe-6d19333af8b5.png" width="60%" height="60%">  


基于Headscale实现的具有WebUI的Tailscale控制服务器       
    
**注意** 此版本可能与Headscale、Tailscale官方版本均有不兼容的情况，如需与官方版本并用需要考虑进行兼容性测试。    
      
      
# 使用方法

## 直接运行
编译后直接运行程序即可。当前启动模型是：

* 进程启动后**先只启动超管 cockpit**，默认监听 ```8081``` 端口；如需避免冲突，可在启动前设置环境变量，例如 ```export MIRAGE_SYS_ADDR="127.0.0.1:18081"```
* cockpit 入口为 ```/cockpit```，例如 ```http://127.0.0.1:8081/cockpit/```
* 首次启动因为还没有有效系统配置，**控制器服务不会自动启动**；需要先进入 cockpit 完成超级管理员绑定（WebAuthn）和基础配置
* cockpit 中基础配置有效后，控制器服务才会启动；默认控制器监听地址为 ```8080```
* 要使控制器具备实际使用价值，至少需要在 cockpit 上配置服务域名；如需完整用户登录体验，通常还需要继续配置一种或多种第三方身份服务商
* 最新版本已经弃用了原本的 URL 读取外部 DERP 列表方式；为了连通性和可用性，建议在正式用户/租户登入前配置至少一条司南信息

## 本地 Podman 部署（已验证可启动 cockpit）
仓库根目录现已提供顶层 `Containerfile`，可直接在本地 Podman 中构建 MirageServer：

```bash
cd /home/hao/A-1/MirageServer
podman build -t mirageserver-local -f ./Containerfile .
mkdir -p /tmp/mirageserver-data
podman run -d \
  --name mirageserver-local \
  -p 18081:8081 \
  -p 18080:8080 \
  -v /tmp/mirageserver-data:/var/lib/mirageserver \
  localhost/mirageserver-local:latest
```

当前已验证：

* 容器可以正常启动，日志会输出 `Cockpit is ready on :8081`
* `http://127.0.0.1:18081/cockpit/` 可返回 `200 OK`
* 在尚未通过 cockpit 完成基础配置并启动服务前，`18080` 控制器端口不会响应
* 挂载目录中会生成并保留 `db.sqlite`，容器删除后重建仍可复用该数据目录

建议的本地持久化约定：

* `db.sqlite`：必须持久化，否则超管绑定和系统配置会在容器重建后丢失
* `download/`：如果要测试客户端发布/下载链路，也建议一并持久化

当前 Podman 本地测试的边界：

* 适合验证 cockpit、首次绑定、基础配置、服务启动、数据库持久化
* **不等于完整线上认证回归**；当前用户侧登录、Dex/OIDC 回调、下载发布 URL 等多处仍依赖 ```https://<ServerURL>```、真实域名和 `Secure` Cookie
* 因此纯本地 HTTP 容器测试更适合作为部署烟测，而不是完整生产登录链路验证

## Windows 客户端安装器现状
Windows 客户端安装器资产已经存在于 `MirageNavi` 仓库中，当前主链路是：

* `mirageclient-win/build-installer.ps1`
* `mirageclient-win/MirageSetup.iss`
* `mirageclient-win/service_install.go`

当前已确认：

* 现有脚本会先生成 Windows 资源，再执行 `GOOS=windows GOARCH=amd64 go build`，最后调用 Inno Setup 生成安装器
* 在当前 Linux 主机上，已可交叉编译出 Windows `Mirage.exe`
* 但最终 `setup.exe` 仍依赖 Windows 原生工具链（至少需要 PowerShell + `ISCC.exe`），因此最终安装器打包应在 Windows 主机或 Windows CI runner 上完成

## ACL 能力现状
当前 MirageServer 的 **ACL 后端能力** 与 **Web UI 已暴露能力** 不是一回事：

* 后端 policy 结构已经支持 `groups`、`hosts`、`tagOwners`、`acls`、`tests`、`autoApprovers`、`ssh`
* 当前 Web UI / 管理 API 里真正完成并可直接操作的，主要还是**标签管理**
* 因此阅读下方功能进度时，应理解为：ACL 页签已经存在，但不代表 ACL 全部策略能力都已经完成可视化配置

# 完成功能进度(ToDo List)：
- [x] 系统零配置文件启动，页面初始化功能   
- [x] 超管驾驶舱   
    - [x] 超管绑定/登录/登出     
    - [x] 系统运行状态展示与控制    
    - [x] 配置页   
        - [x] 系统基本配置   
        - [x] 三方服务配置【后续需要进行调整】   
        - [x] 超管换绑      
        - [x] 客户端版本发布      
    - [x] 租户页   
        - [x] 租户列表展示   
        - [x] 删除租户   
        - [x] 编辑租户(owner、provider、名称、magicDomain)       
        - [x] 租户用量信息   
    - [x] DERP页   
        - [x] 全局DERP服务配置  
        - [x] DERP服务自动化部署   
        - [ ] DERP详情页 
    - [ ] 用户页?   
        - [ ] 个人租户信息展示与管理？  
    - [x] 系统日志页   
- [x] 注册与登录   
    - [x] Google、Microsoft、GitHub、Apple账号登录
    - [x] ~~OIDC对接阿里云IDaaS登录【暂时弃用】~~   
    - [x] ~~对接阿里云手机号注册【暂时弃用】~~   
    - [ ] 个人微信/QQ/微博登录   
    - [ ] 企业微信/钉钉对接登录    
- [x] 主界面框架与页头部用户基本信息展示、登出     
- [x] 设备页签      
    - [x] 设备列表信息展示      
    - [x] 单个设备详情页展示      
    - [x] 设备IP复制与设备控制菜单展示      
    - [x] 修改设备名      
    - [x] 启/停用设备密钥过期      
    - [x] 编辑设备子网路由    
    - [x] 删除设备      
    - [x] 编辑设备ACL标签        
    - [x] 客户端版本更新提示   
    - [ ] 分享设备
- [ ] 服务页签【暂不考虑】    
- [x] 用户页签   
    - [x] 用户列表展示   
    - [x] 管理员身份转移/添加
    - [ ] 更多种类角色划分编辑
    - [ ] 用户设备筛查
    - [ ] 用户冻结
- [x] ACL页签（当前主要完成标签管理，非完整 ACL 可视化管理）
    - [x] 标签管理
    - [ ] 群组管理
    - [ ] 别名管理？
    - [ ] ACL规则条目编辑
    - [ ] 其他后端已支持能力的 UI 暴露（如 auto-approvers / ssh / tests 等）
- [x] DNS页签      
    - [x] 基本信息展示      
    - [x] 启/停用MagicDNS     
    - [x] 启/停用Override Local    
    - [x] 添加全球域名服务器   
    - [x] 添加split域名服务器   
    - [x] 修改/删除域名服务器   
    - [x] Basedomain解绑用户名以及可修改   
    - [x] Split域名服务器可调整顺序   
    - [ ] ~~DNS厂商预植【暂不考虑】~~   
    - [ ] ~~HTTPS证书（BETA）【暂不考虑】~~    
    - [ ] ~~NextDNS支持【暂不考虑】~~    
- [x] DERP页签
    - [x] 组织DERP列表展示
    - [x] 组织DERP列表基本操作接口
    - [x] 全局DERP的区别展示
    - [x] 全局DERP区域的禁用操作
- [x] 设置页签       
    - [x] 通用设置 （暂时不处理设备需预授权开关）
        - [x] 组织名称
        - [x] 身份提供商
        - [x] 设备默认秘钥过期时长     
    - [x] 账单及用量显示   
    - [x] 密钥管理   
        - [x] 授权密钥管理【需要考量这个与用户绑定关系】    
        - [ ] ~~API密钥管理【暂不考虑】~~     
    - [ ] 特性功能开关    
    - [ ] ~~webhook【暂不考虑】~~  
    - [ ] ~~OAuth Client【暂不考虑】~~    
- [x] 组织切分及身份源管理   
    - [x] 按组织划分最终用户    
    - [x] 组织管理员配置   
    - [x] DNS、ACL及其他有关系统配置归属至组织   
- [ ] ~~日志页签【暂不考虑】~~    
- [ ] ~~i18n【暂不考虑】~~   
- [ ] ~~前后端分离【是否有必要？】【暂不考虑】~~      
    
  
# 截图    
    
## 超管界面    
    
<img src="https://user-images.githubusercontent.com/7601383/226161921-0df684fa-4956-4681-b5d1-119e5f434246.png" width="50%" height="50%">
    
    
## 管理员（当前是用户）界面    
       
<img src="https://user-images.githubusercontent.com/7601383/218957899-89ba2492-8508-40aa-b6c7-92d57ecde6d5.png" width="80%" height="80%">   
<img src="https://user-images.githubusercontent.com/7601383/218959833-b2e70903-5250-4fd1-b175-a6b29aef9199.png" width="80%" height="80%">    
<img src="https://user-images.githubusercontent.com/7601383/218959838-2fc1ba9d-b372-4890-806e-28a0ce5c4928.png" width="80%" height="80%">    
<img src="https://user-images.githubusercontent.com/7601383/218959854-b3d02123-b917-4f06-a8cb-a619f591cd42.png" width="80%" height="80%">    
<img src="https://user-images.githubusercontent.com/7601383/218959858-83f4f57e-dfa6-4886-bd1d-e608b8e7b27c.png" width="80%" height="80%">    
<img src="https://user-images.githubusercontent.com/7601383/218959865-35cc93ea-953a-451e-881c-8b216518971d.png" width="80%" height="80%">    
<img src="https://user-images.githubusercontent.com/7601383/218959876-32b0c444-8be4-4372-89f8-05a50c9f775c.png" width="80%" height="80%">    
<img src="https://user-images.githubusercontent.com/7601383/226161978-04a80f9a-5a79-4a09-823a-e76d921cd629.png" width="50%" height="50%">
<img src="https://user-images.githubusercontent.com/7601383/226162115-2aa9aad3-48aa-4a86-8608-4d82058f07fa.png" width="50%" height="50%">
<img src="https://user-images.githubusercontent.com/7601383/226162138-7a5f2d4b-f699-40da-a2a2-7cd7f9dd8c39.png" width="50%" height="50%">
<img src="https://user-images.githubusercontent.com/7601383/218959888-36064015-1052-454b-a5ad-854ceaabe363.png" width="80%" height="80%">    
<img src="https://user-images.githubusercontent.com/7601383/218959893-8c398c91-bddc-4176-836f-ebc6806567b0.png" width="80%" height="80%">    
    

      
感谢Tailscale、Headscale及开源社区的共同努力。    
      
