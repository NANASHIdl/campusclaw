# CampusClaw 项目长期记忆

## 课程与项目背景
- 课程：《互联网软件开发技术与实践》（北大，主讲 张齐勋），学期项目为 **CampusClaw**——面向中小学的教研智能体，覆盖学情分析、备课、命题、课堂与教研协同。
- 项目采用 **SDD（规约驱动开发）** 方法 + **OpenSpec** 工具链。

## 项目约定（来自第 2 课课件）
- 一次变更 = 一个 change，规划产物放在 `openspec/changes/<change>/`，包含四件套：
  - `proposal.md`：Why / What Changes / Non-goals / Capabilities / Impact（做什么、不做什么）
  - `design.md`：技术决策、数据流、数据模型、部署（怎么落地）
  - `specs/<capability>/spec.md`：ADDED Requirements + Requirement + Scenario（WHEN/THEN/AND），每条必须可判定通过/不通过
  - `tasks.md`：分节 checklist（每条带 verify），checkbox 初稿全为 `[ ]`
- 归档后 change 移入 `openspec/changes/archive/`，规约合并进 `openspec/specs/`（长期真相源）。
- OpenSpec 官方五步：Explore（可选）→ Propose → Review → Apply → Archive。
- 仓库根含：`AGENTS.md`（规则：无规约不写代码）、`README.md`（三行：价值 / 场景 / 不做）、`openspec/config.yaml`。
- IDE 三层入口：规则（README + config.yaml）· 技能（.cursor/skills/.../SKILL.md）· 命令（/opsx-propose 等）。
- 环境：node ≥ v20；`npm install -g @fission-ai/openspec@latest`。

## 第 2 课当堂交付物（本次）
- `openspec/changes/<change>/` 下四份规约文件，内容须覆盖：**登录、角色权限、班级隔离、上传入库**；本课不写业务代码（不 Apply）。
- CampusClaw 学期仓命名：`campusclaw-<组名>`；课表练习仓（`sdd-lab-timetable`）不计入交付，两仓不得混用。

## 已实现项目（本目录）
- **技术栈（最轻量）**：Flask + SQLite（标准库 sqlite3）+ Jinja2 + `werkzeug.security`；运行时依赖仅 Flask；生产入口 gunicorn（仅容器）。
- **目录**：`app/`（__init__ 工厂、auth、materials、db、knowledge、env、templates）、`scripts/init_db.py`、`run.py`、`Dockerfile`、`docker-compose.yml`、`.env.example`、`AGENTS.md`。
- **当前 change**：`openspec/changes/add-auth-rbac-class-knowledge/`（含 auth-upload capability）。已 Apply（代码已实现并验收）。
- **约定**：跨班访问统一 **403**；`class_id` 只取自 session；密码哈希（werkzeug 默认 scrypt）；`SECRET_KEY` 仅环境变量，缺失即启动失败。
- **预置账号**：`teacher_a/teach123`（A 班教师）、`student_a1/stu123`（A 班学生）、`student_b1/stu123`（B 班学生）。
- **本地运行**：`pip install -r requirements.txt` → `cp .env.example .env` → `python scripts/init_db.py` → `python run.py`（:5000）。
- **环境注意**：本机 pip 走代理装不了包，验证用的是系统 Python `E:/python310/python.exe`（已带 Flask 3.1.3）。

