# BENZHI_README

## 项目说明
- 项目：benzhi-project-c8c50572-d048-4067-b95a-583fe7c4d3c8
- 项目用途：面向卫星地面站的授时链校准资格服务，支持参考源溯源、测量评估、超限整改、独立复核和证据包封存。
- Go 工具链：`golang:1.22`
- 前端工具链：无

## 项目描述
- 项目名称：ground-clock-qualification
- 项目介绍：面向卫星地面站授时保障人员的授时链校准资格服务，以一次校准活动为聚合根，管理参考源溯源、测量采集、漂移判定、偏差整改、独立复核和资格证据封存，最终形成可验证的任务授时资格结论。
- 项目概述：面向卫星地面站授时保障人员的授时链校准资格服务，以一次校准活动为聚合根，管理参考源溯源、测量采集、漂移判定、偏差整改、独立复核和资格证据封存，最终形成可验证的任务授时资格结论。
- 核心工作流：授时工程师创建校准活动并锁定地面站、设备和任务窗口，提交参考时钟溯源证据后录入多轮偏差测量；系统依据门限生成漂移评估，存在超限项时必须登记根因、整改动作并完成复测，随后由未参与采集的复核员独立签署，服务签发资格证据包并将活动转为只读封存状态。
- 对外接口：提供版本化 HTTP JSON API，覆盖活动命令、状态查询和证据包下载；服务支持 -addr=127.0.0.1:<port>，默认监听 127.0.0.1:19081，也可读取 PORT 并绑定 127.0.0.1:<PORT>，不得默认绑定 0.0.0.0 或常见低位端口。

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...

cd /app && GOTOOLCHAIN=local go run ./cmd/timesyncd -self-check -addr=127.0.0.1:19081

cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh

./build_benzhi_docker.sh benzhi-project-c8c50572-d048-4067-b95a-583fe7c4d3c8-amd64 linux/amd64

./build_benzhi_docker.sh benzhi-project-c8c50572-d048-4067-b95a-583fe7c4d3c8-arm64 linux/arm64

docker run -it benzhi-project-c8c50572-d048-4067-b95a-583fe7c4d3c8-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/timesyncd -self-check -addr=127.0.0.1:19081`
