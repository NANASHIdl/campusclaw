"""用例加载、按角色取客户端、单脚本独立运行。

run_all.py 与「单独跑某个脚本」共用这里的逻辑，避免两套执行路径跑出两种结果。
"""

import importlib.util
import os
import sys
import traceback

from . import assertx, client, db, env

CASE_FILES = {"POST.py", "GET.py", "PUT.py", "DELETE.py", "PATCH.py"}
CASES_DIR = os.path.join(env.ROOT, "apitest", "cases")

ROLE_CREDENTIALS = {
    "teacher": env.TEACHER,
    "student": env.STUDENT,
    "student_b": env.STUDENT_B,
}


class Env:
    """一次运行的环境：按角色惰性登录，令牌在同一次运行内复用。"""

    def __init__(self, base_url=None):
        self.base_url = base_url or env.BASE_URL
        self._clients = {}
        self._anon = client.Client(self.base_url)

    @property
    def anon(self):
        """未登录客户端。"""
        return self._anon

    def client(self, role):
        if role == "anon":
            return self._anon
        if role not in ROLE_CREDENTIALS:
            raise KeyError(f"未知角色 {role!r}，可用：anon / {list(ROLE_CREDENTIALS)}")
        if role not in self._clients:
            c = client.Client(self.base_url)
            resp = c.login(*ROLE_CREDENTIALS[role])
            if resp.status != 200 or not c.token:
                raise RuntimeError(f"{role} 登录失败：HTTP {resp.status} {resp.text[:120]}")
            self._clients[role] = c
        return self._clients[role]


def discover(root=None):
    """按目录遍历出全部用例脚本，返回 [(相对路径, 绝对路径)]，按路径排序保证顺序稳定。"""
    root = root or CASES_DIR
    found = []
    for dirpath, _dirnames, filenames in os.walk(root):
        for name in sorted(filenames):
            if name in CASE_FILES:
                full = os.path.join(dirpath, name)
                found.append((os.path.relpath(full, root), full))
    return sorted(found)


def load_module(file_path):
    name = "apitest_case_" + os.path.splitext(os.path.basename(file_path))[0].lower()
    name += "_" + str(abs(hash(file_path)) % 100000)
    spec = importlib.util.spec_from_file_location(name, file_path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def case_title(module, rel_path):
    return getattr(module, "NAME", rel_path.replace(os.sep, "/"))


def run_case(file_path, run_env, rel_path=""):
    """执行一个用例脚本，返回 Case 记录。异常也记成一条失败断言，不让整批中断。"""
    module = load_module(file_path)
    case = assertx.Case(case_title(module, rel_path))
    try:
        module.run(run_env, case)
    except Exception:  # noqa: BLE001
        case.check("用例执行未抛异常", False, traceback.format_exc(limit=3))
    return case


def standalone(file_path):
    """`python apitest/cases/api/login/POST.py` 的直接入口。"""
    if not db.ping():
        print("无法回查数据库：请确认已 docker compose up，且本机可执行 docker compose exec。")
        return 2

    case = run_case(file_path, Env())
    print(f"== {case.name} ==")
    for item in case.results:
        mark = "PASS" if item["ok"] else "FAIL"
        detail = f"  ({item['detail']})" if item["detail"] else ""
        print(f"  {mark}  {item['title']}{detail}")
    print(f"-- PASS={case.passed} FAIL={case.failed} --")
    return 0 if case.ok else 1


def bootstrap(file_path):
    """把 apitest 目录加入 sys.path，使 `python apitest/cases/...` 能 import common。"""
    root = os.path.dirname(os.path.abspath(file_path))
    while root and not os.path.isdir(os.path.join(root, "common")):
        parent = os.path.dirname(root)
        if parent == root:
            break
        root = parent
    if root not in sys.path:
        sys.path.insert(0, root)
    return root
