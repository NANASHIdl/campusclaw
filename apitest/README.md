# apitest —— 接口自动化测试工程

一个接口一个脚本，目录按 URL 组织；断言不只比状态码，还要核对响应内容并回查数据库。

## 目录

```
apitest/
  common/
    env.py      读取仓库根目录的 .env，给出 BASE_URL 与预置账号
    client.py   HTTP 客户端：Bearer 令牌、JSON 收发、SSE 解帧
    db.py       数据库回查（经 docker compose exec db mysql）
    assertx.py  断言记录器：状态码 / 响应内容 / 数据库三类
    runner.py   用例发现、按角色取客户端、单脚本独立运行
  cases/api/    目录即 URL：cases/api/login/POST.py 表示 POST /api/login
  run_all.py    一键执行并生成 report/index.html
  report/       生成的报告（不入库）
```

## 用例清单

| 脚本 | 覆盖 |
| --- | --- |
| `cases/api/login/POST.py` | 登录换取 JWT、载荷只含注册声明、失败口径统一 |
| `cases/api/search/POST.py` | 混合检索、命中项可回溯、跨班隔离 |
| `cases/api/agents/GET.py` | 智能体列表、教师/学生可见性差异、响应不含 `class_id` |
| `cases/api/agents/PUT.py` | 提示词读写（提示词是智能体的字段，没有独立的 `/api/prompt` 端点） |
| `cases/api/skills/GET.py` | 技能列表与文件原文回读 |
| `cases/api/skills/POST.py` | 用技能文件新建、解析失败文案、写权限 |
| `cases/api/conversations/POST.py` | 新建会话、智能体锁定、会话隔离 |
| `cases/api/conversations/messages/POST.py` | SSE 流式提问、delta 拼接等于落库全文、标题改写 |

路径含动态段的用例（如 `/api/conversations/{id}/messages`）在脚本里用 `PATH` 常量写全路由，
目录只表达静态部分。

## 运行

```bash
# 全部用例 + HTML 报告
python apitest/run_all.py

# 只跑某一块
python apitest/run_all.py --only skills

# 单独跑一个脚本
python apitest/cases/api/login/POST.py

# 换目标地址（默认按 .env 的 WEB_PORT 拼 http://127.0.0.1:端口）
BASE_URL=http://127.0.0.1:8080 python apitest/run_all.py
```

退出码：`0` 全通过，`1` 有失败，`2` 环境不可用。

## 约定

- 只依赖标准库，不需要安装第三方包。
- 数据库端口不对宿主机开放，回查统一走 `docker compose exec db mysql`；
  需要读含换行的字段时用 `db.text()`（内部走 `HEX()`），否则会被 TSV 切行截断。
- 用例自己造数据、自己清理；没有删除接口的资源（如智能体）用带时间戳的名称避免撞名。
- `report/` 是生成物，已在 `.gitignore` 中忽略。
