# ground-clock-qualification

这是面向卫星地面站授时工程师和独立复核员的授时链校准资格服务。系统以校准活动为聚合根，完成参考源溯源、偏差测量、漂移评估、超限整改复测、独立复核及资格证据包封存，并通过版本化 HTTP JSON API 提供查询和下载。

## 构建、运行与测试

```bash
go test ./...
go run ./cmd/timesyncd -addr=127.0.0.1:19081 -data=timesync.db
go run ./cmd/timesyncd -self-check -addr=127.0.0.1:19081
```

监听地址可通过 `-addr` 或 `PORT` 环境变量配置，默认仅绑定 `127.0.0.1:19081`。

## 主要接口

- `POST /api/v1/campaigns` 创建活动；后续依次使用 `reference`、`measure`、`evaluate`、`remediate`、`review` 和 `archive` 动作完成资格签发闭环。
- 创建和草稿修订可锁定 `measurement_plan`（轮次数、跨度、最大间隔、温湿度和信号状态）；`measure-preflight` 与 `measure` 使用同一套定位规则，方案在出现参考证据或轮次后不可修改。
- `GET /api/v1/campaigns` 支持 `state`、`station_code`、`window_start`、`window_end`、`reviewer_id`、`offset` 和 `limit` 组合筛选。结果固定按 `created_at`、`campaign_id` 排序，返回 `total`，单页最多 100 条。
- `GET /api/v1/campaigns/{id}?include=rounds&device_id={device}` 可按设备查看顺序化原始轮次；`include=all` 返回参考覆盖诊断、评估摘要、偏差和复核历史。
- `GET /api/v1/campaigns/{id}/audit` 支持 `revision_start`、`revision_end`、`action`、`actor` 和分页参数，并同时返回完整审计链校验结果。
- `GET /api/v1/campaigns/{id}/artifact` 下载封存证据包，`GET /api/v1/campaigns/{id}/verify` 校验证据包摘要、审计头、连续 revision 和事件摘要链。
- 创建活动会按半开任务窗口原子检查地面站与设备占用，冲突响应包含重叠区间；草稿证据可通过 `reference-corrections` 留痕替代。
- `POST /api/v1/campaigns/resource-availability` 在建档前只读预演同站点、设备的占用冲突，并给出前后最近的完整空闲时段；正式创建仍以事务内冲突检查为准。
- reference、`reference-batches` 和 `reference-corrections` 会统一校验证书摘要的跨活动规范化指纹；`GET /api/v1/campaigns/{id}/reference-digest-usage?certificate_digest={digest}` 查询有效、已替代和已撤回的使用沿革。
- `amendments` 允许在 `DRAFT` 阶段修订站点、任务窗口、设备和门限，并排除自身重新检查资源冲突；`reference-preflight` 可合并现有证据预演 clock、frequency 覆盖而不写库。
- `measure-preflight` 提供批次时序、跨度与环境质量只读预检；`evaluations` 提供评估历史分页及两版设备结论差异。
- `round-voids` 以只追加记录作废原始轮次，后续仍从 `measure` 补采；`measurement-summary` 按设备和用途返回有效样本统计、缺测与时序异常。统计值和评估指标统一舍入到小数点后 9 位。
- `GET /api/v1/campaigns/{id}/measurement-consistency` 按有效轮次诊断采样离散、共同跳变、设备离群和设备缺测，可使用 `device_id`、`purpose` 筛选且不改变 revision。
- `GET /api/v1/campaigns/{id}/environment-correlation` 在 `MEASURED` 及后续活动上按设备分析温度/湿度与时间/频率偏差的相关系数和线性斜率，支持 `device_id`、`environment_field`、`deviation_metric` 筛选；只读结果包含有效/无效样本计数及定位诊断项。
- `evaluate` 会不可覆盖地保存每台设备三项门限的裕量、触发轮次和基于采样时刻的回归明细，并可在 `evaluations` 查询中按 `device_id`、`metric` 定位。
- `GET /api/v1/campaigns/{id}/evaluation-margins` 按正式评估的已保存指标返回 EXCEEDED、CRITICAL、WATCH、COMFORTABLE 风险裕量，支持 `revision`、`device_id`、`risk_level` 筛选。
- `remediation-plans` 跟踪责任人和到期风险，`retest-attempts` 保存失败及成功复测尝试；结构化 `review` 清单保留历次退回处置。
- `POST/GET /api/v1/campaigns/{id}/remediation-dependencies` 以只追加版本登记偏差前置关系、校验无环图并返回拓扑执行队列；被阻塞偏差不能提交复测。
- `POST /api/v1/campaigns/{id}/cancel` 撤销无轮次活动并释放占用；`reference-withdrawals` 以追加记录撤回证据并重算覆盖，原始材料保持只读。
- `device-baselines` 在首批参考证据前批量登记全部活动设备的型号、资产序列号、固件和校准有效期；`reference-batches` 可原子提交互补的 clock、frequency 溯源证据。
- `sample-exclusions` 只追加单设备原始样本排除记录，测量覆盖、评估、快照和封存统一使用未排除样本；原始轮次和值始终保留。
- `GET /api/v1/campaigns/{id}/evaluations/{revision}/reproducibility` 只读核验正式评估的输入摘要和设备指标。
- `remediation-evidence` 保存 ROOT_CAUSE、CONTAINMENT、CORRECTIVE_ACTION 三类整改执行材料；正式复测闭合前要求最新计划版本的三类材料齐备。
- 复核认领人通过 `review-snapshots` 固定待签 revision 和 SHA-256 摘要，`review` 可携带 `snapshot_digest`、`snapshot_revision` 完成签署。
- `GET /api/v1/campaigns/{id}/artifact-comparison?against_campaign_id={id}` 在完整性校验后比较同站点两个封存周期的设备、门限和最终指标变化。
- `POST /api/v1/campaigns/{id}/evaluation-simulations` 只读模拟候选门限；`GET /api/v1/campaigns/remediation-queue` 按站点、设备、指标、责任人与风险分页汇总整改项。
- 复核退回会生成 `review-findings`，工程师通过 `review-findings/resolutions` 逐项闭合；要求复测的事项只能关联退回后新增且合规的 `retest` 轮次。
- `remediation-preflight` 可在正式写入前检查偏差材料和候选复测；`review-claims` 及 `review-claims/release` 提供有时限、可续期的独立复核认领。
- `archive-preflight` 返回封存材料清单、计数和限时 `readiness_token`，`archive` 在提交前始终重新核对材料与审计链。
- 封存证据包包含 campaign、references、rounds、evaluations、deviations、reviews、audit 七个稳定分段及 SHA-256 manifest；`verify?section_name=rounds` 可执行局部校验。
- `POST /api/v1/campaigns/{id}/qualification-checks` 仅接受 `ARCHIVED` 活动；请求可提交不超过 32 项的 `checks`（`query_id`、`station_code`、`window_start`、`window_end`、`device_ids`），服务先校验 artifact 七分段摘要和连续审计链，再按窗口与查询标识稳定排序返回逐项结论及汇总。单项请求格式继续兼容；该只读操作不产生 revision 或审计记录。`successors` 可从完整的 `ARCHIVED` 活动复制配置创建全新 `DRAFT` 周期，且不复制证据、轮次、复核或资格结论。
- `GET /api/v1/campaigns/{id}/qualification-lineage` 校验前序、直接继任、分叉、站点与窗口沿革，并逐期核验封存产物、七个分段和审计头，返回稳定的 `lineage_digest`。

写动作可通过 `If-Match` 或 `expected_revision` 执行乐观并发控制。参考证据、测量、评估和复核动作支持 `Idempotency-Key`，相同请求安全重放，不同请求复用同一键会返回冲突。活动封存后只允许快照、审计、下载、完整性与资格适用性校验；撤销后只允许详情、审计和参考材料查询。
