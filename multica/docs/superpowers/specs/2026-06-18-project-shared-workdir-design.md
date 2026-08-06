# 项目共享工作目录(Project Shared Working Directory)设计

- 日期:2026-06-18
- 状态:待评审
- 作者:czc + Claude

## 1. 背景与问题

群聊里的 agent 看不到用户的项目代码(贪吃蛇),而在 issue 里能看到。排查结论:

- Multica 的「项目(project)」当前只是**元数据**(标题、issue、成员),**不存放任何代码**。
- agent 在 issue 里运行时,daemon 通过 `GetLastTaskSession(agent_id, issue_id)` 复用**该 issue 上一次任务的临时工作目录**(`daemon.go:1347`),所以同一 issue 内「找得到」自己之前的产物。
- 但每个任务默认拿到的是 `~/multica_workspaces/<ws>/<task-short-id>/workdir/` 这种**一次性目录**,用完被 GC 清理。不同 issue、群聊各自独立,互不相通。
- 实测:贪吃蛇项目下 issue #8 / #12 / #15 **各自从零搭了一份不同实现**(JS / JSX / TS),分散在各自临时目录里。**根本不存在「项目的那份代码」**。
- 群聊有独立的 `chat_session.work_dir` 复用线,和任何 issue 目录都不通 → 群聊看不到。

**根因**:不存在「项目级、持久、共享」的代码载体。

## 2. 目标 / 非目标

### 目标
- 一个项目拥有**一份持久的、所有 agent 共享的代码目录**。
- 该项目下**所有 issue 任务**都在这份目录里干活 → 沉淀成一份代码。
- **群聊**可指定「当前项目」,群聊 agent 进入该项目目录 → 看到同一份代码。
- 全自动、零配置:用户不用手动绑任何目录/仓库。

### 非目标(本期不做)
- 同项目任务**并行**(v1 串行,见 §5)。
- 多 daemon / 多机共享同一项目目录(v1 绑定到首个运行的本地 daemon)。
- 云端 runtime 的项目代码(云端走 git;本特性面向**本地 daemon** 工作区)。
- 让用户把项目指向自己的任意文件夹 / git 仓库(Q1 已选「托管隐藏目录」;未来可加)。

## 3. 已确认的设计决策(来自评审问答)

1. **代码位置**:Multica 托管的隐藏目录,全自动 —— `~/multica_workspaces/projects/<project-id>/workdir`。
2. **并发**:v1 串行(共享目录 + 现有路径锁),并行留 v2。
3. **群聊↔项目**:`chat_session` 增加可空 `project_id`;群聊输入框**上传文件按钮旁**加「当前项目」选择/切换按钮;可随时改。

## 4. 方案选型

- **方案 A(选用):托管 `local_directory` 资源,daemon 惰性 provision。** 复用现有 `project_resource(local_directory)` + daemon 的校验/锁/cd 全套逻辑,改动最小。给资源打 `managed` 标记以区分用户手动附加的目录。
- 方案 B:给 `project` 加 `work_dir` 列 + 全新 daemon 分支。重复造轮子,放弃现成的 local_directory 通路。
- 方案 C:每项目自动 `git init` + 每任务 worktree。能解决并行,但复杂度高,留作 v2 并行方案的备选。

下文均针对**方案 A**。

## 5. 架构

### 5.1 项目目录的 provision(惰性、daemon 侧)

托管目录在 **daemon 所在机器**上,server 无法直接创建(不知道哪台机、家目录路径)。因此**惰性 provision**:

1. 任务 claim 时,若任务归属某 project,且该 project **尚无任何资源**(无 github_repo、无 local_directory),则在 claim 响应里标记 `provision_managed_workdir = true` + 期望相对路径 `projects/<project-id>/workdir`。
2. daemon 收到标记后:在 `WorkspacesRoot` 下创建 `projects/<project-id>/workdir`,然后调用新 API `POST /api/projects/{id}/resources/managed` 自报:`{ resource_type: "local_directory", resource_ref: { local_path: <abs>, daemon_id: <self>, managed: true } }`。
3. server 落库该资源(唯一约束保证幂等;并发首任务靠 `ON CONFLICT DO NOTHING`)。
4. 之后该 project 的任务 claim 命中已存在的 managed local_directory → 走现有 local_directory 通路(校验路径、加锁、cd 进去跑)。

> 单 daemon 场景(当前用户)下,managed 资源固定绑这台 Mac 的 `daemon_id`。多 daemon 留作 v2。

### 5.2 issue 任务

- issue 属于某 project 且该 project 有 managed local_directory → 任务在项目目录里跑(现有 `findLocalDirectoryAssignment` 逻辑直接生效,无需改 issue 分支)。
- issue 无 project,或 project 用的是 github_repo → 维持现状。

### 5.3 群聊任务

- `chat_session.project_id` 为空 → 群聊行为同现状(通用对话,临时目录)。
- 非空 → claim 时只解析**该一个 project** 的 managed local_directory 并放进 `ProjectResources` → daemon cd 进项目目录。
- **修正之前的群聊改动**:上一版「群聊拿工作区**全部**项目资源」在「每项目都有 local_directory」后会让 daemon 遇到**多个 local_directory** 而无法择一。改为:群聊只解析 `chat_session.project_id` 指向的那一个项目。(保留把该项目 github_repo 提升进 Repos 的逻辑。)

### 5.4 并发(v1 串行)

- 复用 daemon 现有 `LocalPathLocker`:同一 managed 路径上的任务串行,其余 `waiting_local_directory` 排队。
- 后果:同项目同时只跑一个任务。可接受(v1);并行见 §9。

### 5.5 GC 豁免

- managed 目录视同 local_directory:GC 只清输出/日志,**不删用户代码**(现有 `local_directory=true` 豁免逻辑已覆盖)。

## 6. 数据模型改动

- `chat_session` 增列:`project_id UUID NULL REFERENCES project(id) ON DELETE SET NULL`。
- managed 标记:在 `local_directory` 的 `resource_ref` JSON 里加 `"managed": true`(无需新列);或在 `project_resource` 加 `managed BOOL DEFAULT false`(更清晰,便于 UI/查询过滤)。**倾向加列**。
- 唯一性:managed 资源依赖现有 `UNIQUE(project_id, resource_type, resource_ref)` + `ON CONFLICT DO NOTHING` 防并发重复。

## 7. 后端改动

- migration:`chat_session.project_id`;`project_resource.managed`。
- sqlc 查询(注:本机 sqlc 未装,改动 SQL 需另装或手写 raw SQL):
  - `UpdateChatSessionProject(session_id, project_id)`。
  - claim 时按 `project_id` 取单项目 managed 资源。
  - provision 自报的 upsert。
- `ClaimTaskByRuntime`:
  - issue 分支:无需改(local_directory 已生效)。
  - chat 分支:用 `cs.ProjectID` 解析单项目资源(替换 §5.3 所述的「全部项目」逻辑)。
  - 通用:无资源的 project → 下发 `provision_managed_workdir`。
- 新 API:`PATCH /api/chat-sessions/{id}`(设/切 project_id);`POST /api/projects/{id}/resources/managed`(daemon 自报)。
- daemon:收到 provision 标记 → 建目录 + 自报;其余复用现有 local_directory 执行路径。

## 8. 前端改动

- 群聊输入框:上传文件按钮旁加「当前项目」按钮 → 弹项目选择器(工作区项目列表 + 「无」)。选中调 `PATCH /api/chat-sessions/{id}` 写 `project_id`,并在 header/composer 显示当前项目名,可随时切。
- core:`chat_session` 类型加 `project_id`;mutation `setChatSessionProject`;群聊查询带出当前项目。
- 项目设置/资源区:managed 目录只读展示(标「自动」),不可像普通 local_directory 那样误删/改路径。

## 9. 迁移与落地

- **贪吃蛇专项**:已把最完整的 TS 版骨架备份到 `~/multica-projects/snake`。落地时:首次为贪吃蛇 provision 出 `~/multica_workspaces/projects/<贪吃蛇-id>/workdir` 后,把备份内容**种子拷贝**进去(一次性),让历史代码并入项目目录。
- 其余已存在项目(世界杯等):无资源 → 下次任务自动 provision 空目录(无历史代码可种子,符合预期)。

## 10. 边界情况

- project 已有 github_repo → **不** provision 托管目录(已有代码源)。
- project 无在线本地 daemon → 无法 provision,群聊/issue 行为回退现状,UI 提示「需要本地 daemon」。
- 云端 runtime 跑该 project 任务 → 托管本地目录不适用;需 github_repo。
- 删除 project → `chat_session.project_id` 置空(ON DELETE SET NULL);磁盘目录由 GC/清理另行处理。
- 群聊切换项目 → 下一次任务进新项目目录;不影响历史消息。

## 11. 测试

- Go:`TestClaimTask_ChatUsesBoundProjectWorkdir`(群聊 project_id → 命中单项目 managed 目录);`TestClaimTask_IssueUsesManagedProjectWorkdir`;provision 自报幂等(并发不重复)。
- 修订上一版 `TestClaimTask_ChatSurfacesWorkspaceProjectResources`:语义已变(群聊只看绑定项目),需重写或替换。
- views:群聊项目选择器组件测试;core mutation 测试。
- i18n:新按钮/选择器文案 4 语言。

## 12. 待评审者确认的开放点

1. managed 标记用**新列** `project_resource.managed` 还是塞进 `resource_ref` JSON?(倾向新列)
2. provision 时机:claim 时下发标记由 daemon 建目录(本设计) vs 别的触发点?
3. 贪吃蛇历史代码种子拷贝:用备份的 **TS 版**(`~/multica-projects/snake`)作为唯一基线,丢弃 #8/#12 的 JS/JSX 版?(倾向是)
4. 群聊未设项目时,要不要默认提示用户选一个,还是静默走通用对话?

## 13. v2 展望

- 并行:§4 方案 C(每项目 git + 每任务 worktree,完成合并回主干)。
- 多 daemon 共享(git 同步 / 远程挂载)。
- 用户自选文件夹 / 绑定已有 git 仓库作为项目目录。
