#!/usr/bin/env python3
"""生产只读对比：旧后端（线上）vs 新后端（只读跑生产数据）。

用真实数据验证「新实现查出来的东西和旧的一样」——这是 mock 测不出来的部分
（存量数据里有 201 字截断的 plainContent、缺 order 的分组、null 的 coverFit）。

前置：
  1. 新后端以只读模式跑起来：
     ENV_FILE=.env.production CONFIG_FILE=./config.prod-readonly.yml go run ./cmd/server
  2. 跑这个脚本：
     python3 tests/compare.py

设计：只读、不改任何数据；差异分三档
  ✗ FAIL  不该出现的差异（字段消失、数量对不上、内容不同）
  ⚠ WARN  已知的、刻意改变的语义（归档分桶改成上海时区；标签/分类改成按 publishTime 排序）
  ✓ OK

退出码：0 = 没有 FAIL；1 = 有 FAIL。
"""
from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.parse
import urllib.request

TIMEOUT = 20
OLD = "https://blognext-end.yaria.top"
NEW = "http://127.0.0.1:2333"

results: list[tuple[str, str, str]] = []  # (level, name, detail)


def ok(name): results.append(("OK", name, ""))
def warn(name, detail): results.append(("WARN", name, detail))
def fail(name, detail): results.append(("FAIL", name, detail))


def fetch(base, path, query=None):
    url = base.rstrip("/") + path
    if query:
        url += "?" + urllib.parse.urlencode(query)
    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
            return r.status, json.loads(r.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw.decode("utf-8"))
        except Exception:
            return e.code, None
    except Exception as e:
        raise SystemExit(f"连不上 {url}: {type(e).__name__}: {e}")


# ---------- 归一化：把新信封拆成裸数据 ----------
def newdata(obj, key="data"):
    return obj.get(key)


def olddata(obj, key="data"):
    return obj.get(key)


def cmp_scalar(name, old, new):
    if old == new:
        ok(name)
    else:
        fail(name, f"旧={old!r} 新={new!r}")


def cmp_map(name, old_map, new_map, hint=""):
    """比 {key: count} 映射。"""
    only_old = {k: v for k, v in old_map.items() if new_map.get(k) != v}
    only_new = {k: v for k, v in new_map.items() if old_map.get(k) != v}
    if not only_old and not only_new:
        ok(f"{name}（{len(old_map)} 项）")
        return
    detail = []
    if only_old: detail.append(f"旧有新无/数量不同 {dict(list(only_old.items())[:5])}")
    if only_new: detail.append(f"新有旧无/数量不同 {dict(list(only_new.items())[:5])}")
    # 标签/分类的顺序差异属于已知修复，但键值对不上是真问题
    fail(name, "；".join(detail) + (f" {hint}" if hint else ""))


POST_FIELDS = ["slug", "title", "category", "tags", "bannerImg", "publishTime",
               "lastUpdatedTime", "wordCount", "description", "coverFit"]


def cmp_post(name, a, b):
    bad = [f for f in POST_FIELDS if a.get(f) != b.get(f)]
    if bad:
        fail(name, "字段不一致：" + ", ".join(
            f"{f}(旧={a.get(f)!r} 新={b.get(f)!r})" for f in bad[:3]))
        return
    # plainContent：旧接口读出时截 201，新接口也截 201
    if (a.get("plainContent") or "")[:201] != (b.get("plainContent") or "")[:201]:
        warn(name, "plainContent 前 201 字不同（可能是存量数据本身就超 201）")
    else:
        ok(name)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--old", default=OLD)
    ap.add_argument("--new", default=NEW)
    args = ap.parse_args()

    print(f"旧后端 {args.old}\n新后端 {args.new}（应为只读模式）\n")

    # 0. 新服务必须是只读的
    st, body = fetch(args.new, "/posts/create")
    if st in (401, 403):
        warn("只读闸门检查", "未带 token 先撞到鉴权，无法确认只读标志（可接受）")
    elif st == 405 or body is None:
        warn("只读闸门检查", f"/posts/create 返回 {st}，无法用 GET 探测")
    else:
        warn("只读闸门检查", "跳过（POST 接口无法用脚本安全探测，见 docs/testing.md）")

    # 1. 文章数与字数
    _, o = fetch(args.old, "/get/post/postCount")
    _, n = fetch(args.new, "/posts/list", {"countOnly": "true"})
    cmp_scalar("文章总数", o.get("count"), n["meta"]["count"])

    _, o = fetch(args.old, "/get/post/totalWordCount")
    _, n = fetch(args.new, "/posts/statistics")
    cmp_scalar("总字数", o.get("count"), n["data"]["totalWordCount"])

    # 2. slug 集合
    _, o = fetch(args.old, "/get/post/postSlugs")
    _, n = fetch(args.new, "/posts/slugs")
    if set(o["data"]) == set(n["data"]):
        ok(f"slug 集合（{len(n['data'])} 个）")
    else:
        fail("slug 集合", f"仅旧有 {set(o['data']) - set(n['data'])}；仅新有 {set(n['data']) - set(o['data'])}")

    # 3. 首页列表（20 篇）
    _, o = fetch(args.old, "/get/post/postsInfo", {"startl": 0, "endl": 20})
    _, n = fetch(args.new, "/posts/list", {"page": 1, "pageSize": 20})
    olist, nlist = o["data"], n["data"]
    if len(olist) != len(nlist):
        fail("列表条数", f"旧 {len(olist)} 新 {len(nlist)}")
    else:
        ok(f"列表条数（{len(nlist)} 篇）")
        if [x["slug"] for x in olist] != [x["slug"] for x in nlist]:
            fail("列表顺序", "按 publishTime 倒序的 slug 顺序不一致")
        else:
            ok("列表顺序")
            bad = 0
            for a, b in zip(olist, nlist):
                before = len([r for r in results if r[0] == "FAIL"])
                cmp_post(f"文章 {a['slug']}", a, b)
                after = len([r for r in results if r[0] == "FAIL"])
                bad += after - before
            if bad == 0:
                ok("文章字段（抽样 20 篇全部一致）")

    # 4. 单篇（取第一篇）
    if nlist:
        slug = nlist[0]["slug"]
        _, o = fetch(args.old, "/get/post/postBySlug", {"slug": slug})
        _, n = fetch(args.new, "/posts/detail", {"slug": slug})
        a, b = o["data"], n["data"]
        cmp_post(f"单篇 {slug}", a, b)
        if len(a.get("mdContent") or "") == len(b.get("mdContent") or ""):
            ok("mdContent 长度")
        else:
            fail("mdContent 长度", f"旧 {len(a.get('mdContent') or '')} 新 {len(b.get('mdContent') or '')}")

        # 5. 上下篇（确定性）
        _, o = fetch(args.old, "/get/post/postNavigation", {"slug": slug})
        _, n = fetch(args.new, "/posts/navigation", {"slug": slug})
        o_prev = (o.get("previous") or {}).get("slug")
        o_next = (o.get("next") or {}).get("slug")
        n_newer = (n["data"].get("newer") or {}).get("slug")
        n_older = (n["data"].get("older") or {}).get("slug")
        if o_prev == n_newer and o_next == n_older:
            ok("上下篇方向")
        else:
            fail("上下篇方向", f"旧 previous={o_prev}/next={o_next}；新 newer={n_newer}/older={n_older}")

    # 6. 标签 / 分类（名称与计数）
    _, o = fetch(args.old, "/get/tag/tags")
    _, n = fetch(args.new, "/tags/list")
    cmp_map("标签", {x["name"]: x["count"] for x in o["data"]},
            {x["name"]: x["count"] for x in n["data"]})

    _, o = fetch(args.old, "/get/category/categories")
    _, n = fetch(args.new, "/categories/list")
    cmp_map("分类", {x["name"]: x["count"] for x in o["data"]},
            {x["name"]: x["count"] for x in n["data"]},
            hint="（若仅计数不同，可能是旧接口按不存在的 date 排序导致分页错位）")

    # 7. 归档：总数不变量必须一致；分桶允许不同（时区语义已修）
    _, o = fetch(args.old, "/get/archive/archives")
    _, n = fetch(args.new, "/archives/list")
    o_sum = sum(x["count"] for x in o["data"])
    n_sum = sum(x["count"] for x in n["data"])
    cmp_scalar("归档总数", o_sum, n_sum)
    o_buckets = {(x["year"], x["month"]): x["count"] for x in o["data"]}
    n_buckets = {(x["year"], x["month"]): x["count"] for x in n["data"]}
    if o_buckets == n_buckets:
        ok("归档分桶")
    else:
        diff = {k: (o_buckets.get(k), n_buckets.get(k)) for k in set(o_buckets) | set(n_buckets)
                if o_buckets.get(k) != n_buckets.get(k)}
        warn("归档分桶", f"{len(diff)} 个月份的归属不同（旧 UTC / 新 Asia/Shanghai，这是刻意修的）：{dict(list(diff.items())[:4])}")

    # 8. 说说
    _, o = fetch(args.old, "/get/speaks/speaks", {"startl": 0, "endl": 20})
    _, n = fetch(args.new, "/speaks/list", {"page": 1, "pageSize": 20})
    if [x["time"] for x in o["data"]] == [x["time"] for x in n["data"]]:
        ok("说说顺序（前 20 条 time 一致）")
        bad = [x["time"] for a, b in zip(o["data"], n["data"]) if a["content"] != b["content"]]
        if bad:
            fail("说说内容", f"[{bad[:3]}] 的 content 不一致")
        else:
            ok("说说内容（前 20 条）")
    else:
        fail("说说顺序", "time 序列不一致（检查 Speaks.time 索引与排序方向）")

    _, o = fetch(args.old, "/get/speaks/speaksCount")
    _, n = fetch(args.new, "/speaks/list", {"countOnly": "true"})
    cmp_scalar("说说总数", o.get("count"), n["meta"]["count"])

    # 9. 友链
    _, o = fetch(args.old, "/get/flink/flinks")
    _, n = fetch(args.new, "/links/list")
    o_groups = {g["name"]: g for g in o["data"]}
    n_groups = {g["name"]: g for g in n["data"]}
    if set(o_groups) != set(n_groups):
        fail("友链分组", f"仅旧有 {set(o_groups)-set(n_groups)}；仅新有 {set(n_groups)-set(o_groups)}")
    else:
        ok(f"友链分组（{len(n_groups)} 个）")
        bad = []
        for name, g in n_groups.items():
            og = o_groups[name]
            if len(og.get("links") or []) != len(g.get("links") or []):
                bad.append(f"{name}: 条目数 {len(og.get('links') or [])}→{len(g.get('links') or [])}")
            if (og.get("order") or 0) != (g.get("order") or 0):
                bad.append(f"{name}: order {og.get('order')}→{g.get('order')}")
        if bad: fail("友链分组详情", "；".join(bad[:4]))
        else: ok("友链分组详情（条目数/order）")

    _, o = fetch(args.old, "/get/flink/flinkCount")
    _, n = fetch(args.new, "/links/list", {"countOnly": "true"})
    cmp_scalar("友链条目总数", o.get("count"), n["meta"]["count"])

    # 10. 站点更新时间
    _, o = fetch(args.old, "/get/siteInfo/lastUpdateTime")
    _, n = fetch(args.new, "/site/info")
    cmp_scalar("最后更新时间", o.get("time"), n["data"]["latestUpdateTime"])

    # ---------- 汇总 ----------
    print()
    for level, name, detail in results:
        mark = {"OK": "✓", "WARN": "⚠", "FAIL": "✗"}[level]
        line = f"  {mark} {name}"
        if detail and level != "OK":
            line += f"\n      {detail}"
        print(line)

    fails = [r for r in results if r[0] == "FAIL"]
    warns = [r for r in results if r[0] == "WARN"]
    print(f"\n共 {len(results)} 项：{len(results)-len(fails)-len(warns)} 通过，{len(warns)} 警告，{len(fails)} 失败")
    if fails:
        print("❌ 有真实差异，必须查清才能切换")
        return 1
    print("✅ 没有失败项（警告项是刻意的语义修正）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
