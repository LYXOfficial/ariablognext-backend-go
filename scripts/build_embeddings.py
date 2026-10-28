#!/usr/bin/env python3
"""语义搜索向量回填（增量）。

为什么是独立 Python 脚本而不是 Go 命令：
  - Go 版要过 config.yml 的启动校验，会被逼着配齐 S3/SMTP/GitHub 等
    十几二十个根本用不到的密钥（「变量未定义 = 拒绝启动」）；
  - 清洗后的 searchText 已经由 Go 服务写进 Posts 了，这里直接读，
    不需要在另一种语言里复刻 Markdown 清洗逻辑。

用法（只需 2 个环境变量；库名默认 AriaBlogNext，可用 DB_NAME 覆盖到 dev/test 库）：
    MONGODB_URI=... SF_TOKEN=... python3 scripts/build_embeddings.py
    ... python3 scripts/build_embeddings.py --force   # 忽略 hash，全量重算

增量判定（与 Go 版语义一致）：文档里存的 textHash 与当前 searchText 的
sha256 前 16 位不同（或 model/dim 不匹配、或缺失）才重算。

依赖：pymongo[srv]（Atlas 的 SRV 连接需要 dns 解析）、certifi。
"""
import argparse
import hashlib
import html
import json
import math
import os
import re
import struct
import sys
import time
import urllib.request

from typing import Any

from pymongo import MongoClient
import certifi

ENDPOINT = "https://api.siliconflow.cn/v1/embeddings"
MODEL = "BAAI/bge-m3"
DIM = 1024
# 库名与模型一样是长期常量：生产库就是 AriaBlogNext。
# 允许用 DB_NAME 覆盖只是为了本地/测试库（*_dev / *_test）跑同一份脚本。
DB_NAME = os.environ.get("DB_NAME", "").strip() or "AriaBlogNext"
CHUNK = 6000      # 与 Go 版一致：约 6000 字符一块
BATCH = 16        # 单次 API 请求条数
COLLECTION = "PostEmbeddings"


def env(name: str) -> str:
    v = os.environ.get(name, "").strip()
    if not v:
        print(f"缺少环境变量 {name}", file=sys.stderr)
        sys.exit(1)
    return v


def embed(texts: list[str], api_key: str, retries: int = 3) -> list[list[float]]:
    """批量向量化并 L2 归一化（归一化后余弦 = 点积，与 Go 版一致）。"""
    body = json.dumps({"model": MODEL, "input": texts}).encode()
    for attempt in range(retries):
        try:
            req = urllib.request.Request(
                ENDPOINT, data=body, method="POST",
                headers={"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"})
            with urllib.request.urlopen(req, timeout=60) as resp:
                data = json.load(resp)["data"]
            # 显式标注：不加注解时 [None]*n 会被推断成 list[None]，
            # 后面赋浮点数组就报 __setitem__ 类型不匹配（Pyright/Pylance）
            out: list[list[float] | None] = [None] * len(texts)
            for d in data:
                v = d["embedding"]
                if len(v) != DIM:
                    raise RuntimeError(f"维度不符：配置 {DIM}，返回 {len(v)}")
                n = math.sqrt(sum(x * x for x in v)) or 1.0
                # 量化到 float32 精度：Go 端按 []float32 读取，全精度 double 虽然
                # 有 truncate tag 兜底，但两边精度对齐后行为更可预期
                out[d["index"]] = [struct.unpack("f", struct.pack("f", x / n))[0] for x in v]
            if any(v is None for v in out):
                raise RuntimeError("返回条数不足")
            return [v for v in out if v is not None]
        except Exception as e:  # noqa: BLE001
            if attempt == retries - 1:
                raise
            print(f"  向量化失败，{3}s 后重试（{attempt + 1}/{retries}）：{e}", file=sys.stderr)
            time.sleep(3)
    raise AssertionError("unreachable")


TAG_RE = re.compile(r"<[^>]+>")
# 轻量清洗：只做「不影响语义」的处理（去 HTML 标签、去常见 Markdown 标记、
# 压空白、转小写）。不追求与 Go 的 textutil.BuildSearchText 逐字节一致——
# 那份实现有 300 行表格/围栏/行内语法细节，复刻它纯属自找维护负担；
# 向量只需要「内容词都在、噪声少」，与查询侧（原始 query）比较时差别可忽略。
MD_MARK_RE = re.compile(r"[#>*_`~\[\]()!|\-]{1,}")


def clean_text(title: str, md: str) -> str:
    text = html.unescape(TAG_RE.sub(" ", f"{title}\n{md}"))
    text = MD_MARK_RE.sub(" ", text)
    return re.sub(r"\s+", " ", text).strip().lower()


def source_text(p: dict[str, object]) -> str:
    """嵌入源文本：优先用服务端维护的 searchText（迁移后改过正文的文章才有），
    否则从 mdContent 轻量清洗兜底（存量文章从没被改过，库里没有 searchText）。"""
    st = str(p.get("searchText") or "").strip()
    if st:
        return st
    return clean_text(str(p.get("title") or ""), str(p.get("mdContent") or ""))


def chunks_of(text: str) -> list[str]:
    return [text[i:i + CHUNK] for i in range(0, len(text), CHUNK)] or []


def text_hash(text: str) -> str:
    return hashlib.sha256(text.encode()).hexdigest()[:16]


def main() -> None:
    ap = argparse.ArgumentParser(description="增量回填 PostEmbeddings")
    ap.add_argument("--force", action="store_true", help="忽略 textHash，全量重算")
    args = ap.parse_args()

    uri, api_key = env("MONGODB_URI"), env("SF_TOKEN")
    client: MongoClient[dict[str, Any]] = MongoClient(
        uri, tlsCAFile=certifi.where(), serverSelectionTimeoutMS=15000)
    db = client[DB_NAME]
    print(f"目标库：{DB_NAME}")
    posts = list(db.Posts.find({}, {"_id": 0, "slug": 1, "title": 1, "searchText": 1, "mdContent": 1}))
    existing = {d["_id"]: d for d in db[COLLECTION].find({}, {"chunks": 0})}
    print(f"文章 {len(posts)} 篇，已有向量 {len(existing)} 篇")

    computed = skipped = failed = 0
    batch: list[tuple[str, str, list[str]]] = []   # (slug, hash, chunks)
    batch_texts: list[str] = []

    def flush() -> None:
        nonlocal computed, failed, batch, batch_texts
        if not batch_texts:
            return
        vectors = embed(batch_texts, api_key)
        cursor = 0
        now = int(time.time())
        for slug, h, cs in batch:
            doc = {
                "_id": slug, "model": MODEL, "dim": DIM, "textHash": h,
                "updatedAt": now,
                "chunks": [
                    {"idx": i, "text": cs[i], "vector": vectors[cursor + i]}
                    for i in range(len(cs))
                ],
            }
            cursor += len(cs)
            try:
                db[COLLECTION].replace_one({"_id": slug}, doc, upsert=True)
                computed += 1
            except Exception as e:  # noqa: BLE001
                failed += 1
                print(f"  写入失败 {slug}：{e}", file=sys.stderr)
        batch, batch_texts = [], []

    for p in posts:
        text = source_text(p)
        if not text:
            skipped += 1
            print(f"  跳过（无可用正文）：{p['slug']}")
            continue
        h = text_hash(text)
        old = existing.get(p["slug"])
        if old and not args.force and old.get("model") == MODEL \
                and old.get("dim") == DIM and old.get("textHash") == h:
            skipped += 1
            continue
        cs = chunks_of(text)
        batch.append((p["slug"], h, cs))
        batch_texts.extend(cs)
        if len(batch_texts) >= BATCH:
            flush()

    flush()
    print(f"完成：computed={computed} skipped={skipped} failed={failed} model={MODEL}")
    client.close()
    if failed:
        sys.exit(1)


if __name__ == "__main__":
    main()
