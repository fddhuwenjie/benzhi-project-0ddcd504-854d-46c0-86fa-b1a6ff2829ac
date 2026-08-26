# BENZHI_README

## 项目说明
- 项目：benzhi-project-0ddcd504-854d-46c0-86fa-b1a6ff2829ac
- 项目用途：声档封存 ArchiveSeal 提供声音载体数字化质量治理、重采追踪与只读保存包封存的版本化 HTTP JSON API。
- Go 工具链：`golang:1.22`
- 前端工具链：无

## 项目描述
- 项目名称：声档封存 ArchiveSeal
- 项目介绍：面向声音档案数字化团队的质量治理服务，将一件历史录音载体从接收登记、状况评估、采集规划、数字化取证、质量复核与必要的重采，推进到可验证且只读的保存包封存状态。
- 项目概述：面向声音档案数字化团队的质量治理服务，将一件历史录音载体从接收登记、状况评估、采集规划、数字化取证、质量复核与必要的重采，推进到可验证且只读的保存包封存状态。
- 核心工作流：载体以 REGISTERED 状态建档，经状况评估进入 ASSESSED、采集方案批准进入 READY_FOR_CAPTURE、采集取证进入 CAPTURED；质量不合格时转入 RECAPTURE_REQUIRED 并在授权后重采，质量通过后进入 QC_PASSED，最终生成可验证保存包并转为 SEALED，封存后禁止业务修改。
- 对外接口：仅提供版本化 HTTP JSON API；Go 服务默认监听 127.0.0.1:19081，支持 -addr=127.0.0.1:<port>，并在未显式传入 -addr 时读取 PORT 且绑定 127.0.0.1:<PORT>，绝不默认绑定 0.0.0.0、8080、80 或 3000。

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...

cd /app && GOTOOLCHAIN=local go run ./cmd/archiveflow -self-check -addr=127.0.0.1:19081

cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh

./build_benzhi_docker.sh benzhi-project-0ddcd504-854d-46c0-86fa-b1a6ff2829ac-amd64 linux/amd64

./build_benzhi_docker.sh benzhi-project-0ddcd504-854d-46c0-86fa-b1a6ff2829ac-arm64 linux/arm64

docker run -it benzhi-project-0ddcd504-854d-46c0-86fa-b1a6ff2829ac-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/archiveflow -self-check -addr=127.0.0.1:19081`
