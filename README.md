# 声档封存 ArchiveSeal

面向声音档案数字化团队的质量治理 HTTP JSON 服务，覆盖载体登记、状况评估、采集方案、采集证据、质量复核、重采和保存包封存。

运行测试：`go test ./...`

启动服务：`go run ./cmd/archiveflow -addr=127.0.0.1:19081`。也可通过 `PORT` 指定端口。

公开流程依次使用 `POST /v1/cases`、`/v1/cases/{case_id}/assessment`、`/plan`、`/captures`、`/quality`、`/recaptures` 和 `/seal`。所有写请求都必须提供非空 `request_id`（也可使用 `X-Request-ID` 请求头）；除建档外还必须提供 `expected_revision`。相同 `request_id` 的成功请求会返回首次结果，修订冲突返回 HTTP 409。

`POST /v1/cases` 除兼容原有单件载荷外，还接受 `mode`（`atomic` 或 `partial`）和最多 100 个 `items`。服务逐项返回 `created`、`invalid` 或 `conflict`；原子模式有任一错误即整批不写入，逐项模式只写入合法项目。批次 `request_id` 的回执会随快照持久化，重试不会重复建档或追加审计事件。

单件和批次建档都可在同一载荷提交 `intake_receipt`，记录移交单位、移交人、接收人、接收时间、交接批次号和包装状况。服务会规范化文本及批次号，拒绝未来接收时间和交接双方为同一人，并把 `receipt_digest` 固化到登记快照和首个审计事件。

建档还可提交按时间递增的 `custody_events`，逐段记录交出人、接收人、发生时间、规范化库位代码、封签状态和交接说明。系统核验相邻责任人衔接、库位代码唯一性、建档时间上界及接收凭证首尾双方，计算 `current_custodian`、`current_location_code` 和 `custody_chain_digest`；单件与批次都会随登记快照和首个审计事件原子保存。

可通过只读接口 `GET /v1/cases/{case_id}/custody` 查看交接链。接口支持 `from_time`、`to_time`、`limit`（最多 100）和 `include_events` 参数，返回当前责任人、库位、封签链摘要、审计头及 `integrity_status`；查询会重算交接摘要并校验审计链和封存清单锚点，不产生任何持久化写入。

建档还可提交 `alternative_identifiers`，类型支持 `LEGACY_ACCESSION`、`SHELFMARK` 和 `CARRIER_BARCODE`。替代标识经大小写和空白规范化后与主馆藏号共同执行全局唯一检查；`GET /v1/cases?alternative_identifier=...` 可精确定位个案并返回命中的标识来源。

建档载荷可提交 `carrier_facets`，逐项登记唯一 `facet_id`、标签、从一开始连续的 `physical_order`、内容范围和可播放性。系统按物理序号规范化清单并保存 `carrier_facets_digest`；显式分面清单不得为空、重复或全部不可播放。未提交该字段的兼容请求会生成一个 `MAIN` 主分面。

状况评估通过 `treatment_evidence` 为每个霉变、断裂、粘连、污染或高播放风险提交类别、动作、执行人、完成时间和证据摘要；无必需处置类别时必须明确设置 `no_treatment_required=true`。采集方案进入 `READY_FOR_CAPTURE` 后、首次采集前仍可向 `/plan` 提交完整新方案和 `revision_reason`，响应提供新 `plan_revision`、`plan_fingerprint` 与 `changed_fields`，查询中的 `plan_history` 保留全部只读版本。

评估的 `damage_locations` 把霉变、断裂、粘连、污染和播放风险落实到登记分面与物理位置，记录严重度、影响比例、观察说明和证据摘要。系统拒绝未知分面、重复位置以及顶层风险声明不一致，按类别返回最高严重度、累计影响比例和稳定摘要；评估更正会保留旧版定位证据。

`ASSESSED` 且尚未批准方案时，可从原评估入口用当前 `assessment_version`、`correction_reason` 和完整替代评估进行更正；成功后评估版本与个案 revision 各递增一次，`assessment_history` 和 `ASSESSMENT_CORRECTED` 审计事件保留前后摘要。方案以 `valid_until` 固化批准窗口（最长 90 天），查询实时返回 `ACTIVE` 或 `EXPIRED`；到期后须用 `reapproval_reason` 完整复批，采集开始时间必须落在当前方案窗口内。

方案可声明 `scheduled_start` 和 `scheduled_end`。时窗必须位于批准有效期内且覆盖预计任务总时长；批准和修订会检查其他未封存个案的播放设备与操作人冲突。有效预约返回 `ACTIVE`，成功采集后为 `CONSUMED`，到期为 `EXPIRED`；修订只有提交成功才释放旧时窗，规范化时窗也进入方案指纹和历史。

尚未开始采集的 `READY_FOR_CAPTURE` 个案可从 `POST /v1/cases/{case_id}/plan/reservation` 提交 `action=RELEASE`、`released_by` 和 `release_reason` 主动释放预约。释放保持个案状态不变但将方案及对应历史项标为 `RELEASED`，记录释放元数据并追加 `PLAN_RESERVATION_RELEASED` 审计事件；旧方案不能再提交采集，须从 `/plan` 以更高版本和 `revision_reason` 重新排期后恢复 `ACTIVE`。

`POST /v1/cases/{case_id}/captures` 支持 `preflight=true` 的单项或 `items` 成组预检。预检要求 `request_id` 与 `expected_revision`，在个案克隆上执行正式采集的方案、校准、文件参数、分段、固化、操作事件和重采授权门禁，返回逐项错误、下一代次、`technical_evidence_digest` 和确定性 `report_digest`；预检不会推进状态、消费授权或追加审计，确认后移除标记再提交正式采集。

需要播放前环境稳定化的评估使用 `acclimatization` 声明温湿度范围、最短稳定分钟数和有序 `readings`；受潮、低温等载体必须提供至少起止两次读数，只有末段连续合格窗口覆盖规定时长才会进入 `ASSESSED`。方案使用 `capture_tasks` 和 `skipped_facets` 准确覆盖全部登记分面，任务顺序连续且声道安排与总方案一致，响应返回覆盖摘要和预计总时长。

评估的 `observation_evidence` 以唯一资产摘要逐项覆盖已识别风险，并生成 `observation_evidence_digest`。方案的 `risk_controls` 必须准确覆盖这些风险；无风险时使用 `no_additional_controls=true` 和空清单。规范化控制项及 `risk_control_digest` 会进入方案指纹和历史版本。

采集载荷必须包含校准设备、`calibrated_at`、`calibration_valid_until`，以及源文件大小、容器、实际编码、采样率、位深和声道数。整个采集窗口须位于有效校准期，设备和文件参数须与当前方案一致，未压缩文件大小也会按时长和音频参数核验。质量载荷的 `defect_markers` 记录削波、掉样和人工听检异常的时间区间、声道与说明；削波和掉样汇总计数必须与规范化明细数量一致。

多任务方案使用 `/captures` 的 `items` 一次提交完整代次，各项独立携带 `capture_task_id` 和技术证据；任务漏项、重复任务或跨项目资产摘要重复会使整组无写入失败。每项可提交 `calibration_profile` 与逐声道 `calibration_measurements`，系统核验测量时间、证书摘要、声道覆盖、电平偏差、声道间差异和参考频率偏差，并固化 `calibration_evidence_digest` 与策略版本。

每个采集任务可用 `file_segments` 提交从一开始连续编号的分段文件，逐段记录源起止位置、时长、大小、资产摘要和首尾连续性标记。系统核验相邻位置无缺口或重叠、汇总时长与大小一致，并用 `sha256(concat(binary_segment_asset_digests))` 复算任务级 `asset_digest`；成组采集任一分段错误会使整代无写入。

采集可用 `capture_task_id` 引用当前方案任务，并以 `fixity_algorithm=sha256`、固定块大小和有序 `fixity_chunks` 提交分块固化证据。组合规则为 `sha256(concat(binary_chunk_digests))`，复算结果必须等于 `asset_digest`。质量请求可提交逐声道 `channel_metrics` 与 `measurement_profile`，系统按载体门限判定直流偏移、响度、噪声底和静音占比；显式 `decision` 必须与计算结果一致。

质量 `defect_markers` 还可为每个削波、掉样或听检异常提交 `severity=MINOR|MAJOR|CRITICAL`。系统按类别与声道合并重叠区间，返回去重影响时长、比例、标记数和最高严重度，并固化 `defect_impacts` 摘要。列表可用 `minimum_severity` 筛选失败个案，统计按严重度计数而不改变原质量通过率。

采集的 `operation_events` 支持暂停、恢复、载体清洁、拼接修复和操作备注；暂停须成对、干预须位于暂停区间，净采集时长会与音频时长核对。质量请求的 `listening_intervals` 按声道合并计算覆盖率：普通首采须覆盖开头、中段、结尾及最低抽样比例，高播放风险和重采代次须逐声道全程覆盖，人工听检异常也必须位于听检区间。

质量失败后的 `/recaptures` 使用 `remediations` 逐类覆盖全部 `failure_categories`，并提供独立授权人和 `expires_at`。授权只允许在有效期内消费一次。成功采集后旧质量决定、整改和授权仍按代次只读保留。

整改项可进一步提交 `performed_by`、`completed_at`、`result`、`evidence_digest` 和 `verification_method`，系统核验完成时间位于失败复核之后且不晚于授权、证据摘要在本次授权内唯一，并强制整改执行人与方案操作员、质量复核员和授权人分离。规范化完成证据绑定 `authorization_version`，重采时会校验对应摘要未变化。

重采质量请求可用 `remediation_checks` 逐项核验上一授权，核验人与整改责任人必须分离；响应分别列出 `resolved_categories`、`persistent_categories` 和 `new_categories`，并保存逐代成效摘要。

高播放风险、发生拼接修复或达到多次重采阈值时，首次 `/quality` 提交保持 `CAPTURED` 并返回待会签状态。独立复核员从同一入口提交 `countersign_for_revision`、结论和已确认的质量证据摘要；人员分离且结论一致后才落定状态，分歧材料会保留并允许新 revision 重审。`/recaptures` 的 `action` 支持 `authorize`、`revoke` 和 `renew`，采集只接受最新有效且未消费的 `authorization_version`。

会签为 `DISAGREED` 时，第三名复核员可从质量入口提交 `adjudication_for_revision`、会签 revision、逐项 `disagreement_resolutions` 和终局结论，原主审与会签均保持只读。同一失败类别连续两代出现后，下一次重采必须提交 `escalation`，包括独立保存负责人、风险处置决定和最大新增尝试次数；查询返回触发类别及剩余额度。

`GET /v1/cases/{case_id}` 返回完整个案、历次方案、采集、质量结论和重采记录。`QC_PASSED` 后先调用 `GET /v1/cases/{case_id}/manifest?preview=true`，取得无副作用的 `candidate_manifest_digest`、审计头和阻断原因；`POST /seal` 必须用 `expected_manifest_digest` 确认该候选摘要。封存后可通过 `GET /v1/cases/{case_id}/manifest` 读取保存包清单，通过 `GET /v1/cases/{case_id}/verify` 重新校验清单与审计链完整性。

候选和正式清单都包含固定顺序的 `registration`、`assessment`、`plans`、`captures`、`quality`、`recaptures` 六类 `component_digests`；验证失败会返回精确的 `mismatched_components` 以及总摘要的期望值和实际值。`GET /v1/cases/{case_id}/audit` 支持 `from_time`、`to_time`、`event_type`、`after_revision` 和最大为 100 的 `limit` 组合检索；事件仍按原始 revision 分页，并返回每项 `previous_digest`、`event_digest`、下一游标、当前链头、验证进度和稳定响应摘要。检索会校验完整 revision 链及封存清单审计锚点，发现删改时以 `integrity_error` 和期望/实际摘要返回可诊断断点，且不会修改个案、幂等回执或审计日志。

正式清单的 `manifest` 与 `verify` 查询可用 `component=registration|assessment|plans|captures|quality|recaptures` 投影单个组件；`captures` 还可组合 `generation`。响应包含组件内容、`component_digest`、稳定 `proof_path`、总载荷摘要和分层验证结果，查询不改变封存快照、revision 或审计链。

保存包还包含按代次和任务顺序稳定排列的 `generation_evidence_index`，把每代资产关联到方案版本、采集任务、质量 revision、失败决定和重采授权版本。`GET manifest` 与 `GET verify` 可用 `generation` 查询参数定位单代证据路径，封存和验证都会复核引用完整性。

`GET /v1/cases` 支持 `state`、`accession_prefix`（或 `accession`）、`title`（或 `title_keyword`）、`failure_category`、`sealed_after`、`sealed_before`、`offset` 和 `limit` 组合筛选，并返回稳定排序、过滤前分页总数、失败分类、质量通过率、待复核量、重采和封存统计。

当 `state=SEALED` 时，可同时提交 `integrity_check=true` 和 `limit=1..100` 对当前页执行只读批量巡检。响应的 `integrity_results` 逐案给出 `VALID`、`INVALID` 或 `UNAVAILABLE`，并列出六组件不匹配、代次引用错误、审计锚点错误及总摘要期望值和实际值；`integrity_stats` 同时汇总当前页与全部匹配范围，巡检不会更新 revision、清单状态、幂等回执或审计日志。

采集方案响应包含 `plan_revision` 和确定性 `plan_fingerprint`，后续采集请求必须引用当前 `plan_revision`；如同时提交 `plan_fingerprint`，服务会校验并固化该指纹。所有写接口仅接收 `application/json`，拒绝未知字段；封存后的个案统一只读。
