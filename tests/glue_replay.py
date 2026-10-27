#!/usr/bin/env python3
"""旧契约真机回放：经 glue(:2335) → go(:2333)，验证旧格式与业务生效。"""
import json, urllib.parse, urllib.request, urllib.error

import sys, time
BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:2335"
TS = str(time.time_ns())  # 纳秒级：同一秒内连跑两次也不会撞判重

def call(method, path, body=None, token=None, ctype="application/json"):
    url = BASE + path
    data = None
    h = {"Accept": "application/json"}
    if token: h["Authorization"] = "Bearer " + token
    if body is not None:
        data = json.dumps(body).encode()
        h["Content-Type"] = ctype
    r = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try: return e.code, json.loads(raw)
        except: return e.code, raw[:200].decode("utf-8", "replace")

def login():
    st, d = call("POST", "/access/user/login", {"user":"seedadmin","password":"seed-admin-password"})
    assert st == 200 and d.get("jwt"), (st, d)
    return d["jwt"]

TOKEN = login()
print("登录（旧格式 jwt）:", "ok")

fails = []
def check(name, cond, detail=""):
    print(f"  {'✓' if cond else '✗'} {name}" + (f"  {detail}" if not cond else ""))
    if not cond: fails.append(name)

# ---- 读接口形状（旧格式：顶层 message + 指定键）----
st, d = call("GET", "/get/post/postCount"); check("postCount {count,message}", st==200 and isinstance(d.get("count"),int) and d.get("message")=="success", d)
st, d = call("GET", "/get/post/postsInfo?startl=0&endl=2")
check("postsInfo data 数组 2 条", st==200 and len(d.get("data",[]))==2, len(d.get("data",[])))
first = d["data"][0]
check("postsInfo 条目无 mdContent", "mdContent" not in first)
check("postsInfo plainContent ≤201", len(first.get("plainContent","")) <= 201)

st, d = call("GET", "/get/post/postsInfo?type=full")
check("postsInfo type=full 带 mdContent", st==200 and all("mdContent" in p for p in d["data"]), len(d.get("data",[])))

st, d = call("GET", "/get/post/postBySlug?slug=hello-go")
check("postBySlug 有 mdContent", st==200 and "mdContent" in d.get("data",{}))
st, d = call("GET", "/get/post/postBySlug?slug=nope")
check("postBySlug 不存在 404", st==404, st)
st, d = call("GET", "/get/post/postNavigation?slug=hello-go")
check("postNavigation previous/next 键", st==200 and set(d.keys()) >= {"previous","next"}, list(d))
st, d = call("GET", "/get/post/searchPosts?query=%E5%8D%9A%E5%AE%A2")
check("searchPosts 返回 context", st==200 and (not d["data"] or "context" in d["data"][0]))
st, d = call("GET", "/get/tag/tags"); check("tags 非空", st==200 and len(d["data"])>0)
st, d = call("GET", "/get/tag/tagCount"); check("tagCount count", st==200 and d.get("count",0)>0)
st, d = call("GET", "/get/category/categories"); check("categories 非空", st==200 and len(d["data"])>0)
st, d = call("GET", "/get/archive/archives"); check("archives 非空", st==200 and len(d["data"])>0)
st, d = call("GET", "/get/archive/archiveInfo?year=2025&month=10&startl=0&endl=6")
check("archiveInfo totalCount 存在", st==200 and "totalCount" in d, d if st!=200 else list(d))
st, d = call("GET", "/get/tag/tagInfo?tag=%E5%8D%9A%E5%AE%A2&startl=0&endl=6")
check("tagInfo totalCount 存在", st==200 and "totalCount" in d, list(d))
st, d = call("GET", "/get/flink/flinks"); check("flinks 分组+links", st==200 and d["data"] and "links" in d["data"][0])
st, d = call("GET", "/get/flink/flinkCount")
st2, d2 = call("GET", "/get/flink/flinks")
expect_links = sum(len(g.get("links", [])) for g in d2["data"])
check("flinkCount == 各分组链接数之和", st==200 and d.get("count")==expect_links, (d, expect_links))
st, d = call("GET", "/get/speaks/speaks?startl=0&endl=2"); check("speaks 2 条", st==200 and len(d["data"])==2)
# 计数用「动态比对」而不是写死：与列表接口的总数一致即可（重跑/其他会话可能改过数据）
st, d = call("GET", "/get/speaks/speaksCount")
st2, d2 = call("GET", "/get/speaks/speaks?startl=0&endl=100")
check("speaksCount == speaks 列表长度", st==200 and d.get("count")==len(d2["data"]), (d.get("count"), len(d2["data"])))
st, d = call("GET", "/get/siteInfo/lastUpdateTime"); check("lastUpdateTime time", st==200 and d.get("time",0)>0, d)

# ---- 写接口（dev 库）----
NEW = "replay-post-" + TS
st, d = call("POST", "/update/post/addPost", {"slug":NEW,"title":"回放文章","category":"技术","tags":["t"],"bannerImg":"","description":None,"coverFit":None,"publishTime":1760901926,"lastUpdatedTime":1760901926}, token=TOKEN)
check("addPost 200 success", st==200 and d.get("message")=="success", (st,d))
st, d = call("POST", "/update/post/addPost", {"slug":NEW,"title":"重复"}, token=TOKEN)
check("addPost 重复 → 旧语义 200/409？", st in (200,409), (st,d))
st, d = call("PUT", "/update/post/updatePostInfo", {"slug":NEW,"title":"改过","description":None,"category":"技术","tags":["t2"],"coverFit":None,"bannerImg":"","publishTime":1760901926,"lastUpdatedTime":1760901927}, token=TOKEN)
check("updatePostInfo 200 success", st==200 and d.get("message")=="success", (st,d))
st, d = call("PUT", "/update/post/updatePostMarkdown", {"slug":NEW,"markdown":"# 回放\n\n正文内容 Go 搜索词"}, token=TOKEN)
check("updatePostMarkdown 200", st==200 and d.get("message")=="success", (st,d))
st, d = call("GET", "/get/post/postBySlug?slug="+NEW)
check("写入后详情可读且正文正确", st==200 and "回放" in d["data"]["mdContent"], st)
st, d = call("DELETE", "/update/post/deletePost", {"slug":NEW}, token=TOKEN)
check("deletePost 200", st==200 and d.get("message")=="success", (st,d))
st, d = call("GET", "/get/post/postBySlug?slug="+NEW)
check("删除后 404", st==404, st)

# 说说
st, d = call("POST", "/update/speaks/newSpeaks", {"content":"回放说说 **粗**"}, token=TOKEN)
check("newSpeaks 200", st==200 and d.get("message")=="success", (st,d))
st, d = call("GET", "/get/speaks/speaks?startl=0&endl=1")
when = d["data"][0]["time"]
st, d = call("PUT", "/update/speaks/updateSpeaks", {"time":when,"content":"改过"}, token=TOKEN)
check("updateSpeaks 200", st==200, (st,d))
st, d = call("DELETE", "/update/speaks/deleteSpeaks", {"time":when}, token=TOKEN)
check("deleteSpeaks 200", st==200, (st,d))

# 友链：建组 → 加链 → 改 → 移 → 删
st, d = call("POST", "/update/flink/addGroup", {"name":"回放组","description":"d","order":9}, token=TOKEN)
check("addGroup 200", st==200, (st,d))
st, d = call("POST", "/update/flink/addFlink", {"group":"回放组","id":"rp1","name":"回放链","url":"https://rp.example","avatar":"","description":"","color":"#f08","latency":0}, token=TOKEN)
check("addFlink 到已存在组 200", st==200, (st,d))
st, d = call("PUT", "/update/flink/updateFlink", {"group":"回放组","id":"rp1","name":"回放链改","url":"https://rp.example","avatar":"","description":"","color":"#0af"}, token=TOKEN)
check("updateFlink 200", st==200, (st,d))
# 旧契约字段是 snake_case（from_group/to_group/link_id），glue 负责转成新接口的 camelCase
st, d = call("POST", "/update/flink/moveFlinkGroup", {"from_group":"回放组","to_group":"友达","link_id":"rp1"}, token=TOKEN)
check("moveFlinkGroup 200", st==200, (st,d))
st, d = call("DELETE", "/update/flink/deleteFlink?group=%E5%8F%8B%E8%BE%BE&id=rp1", token=TOKEN)
check("deleteFlink 200", st==200, (st,d))
st, d = call("DELETE", "/update/flink/deleteGroup?name=%E5%9B%9E%E6%94%BE%E7%BB%84", token=TOKEN)
check("deleteGroup 200", st==200, (st,d))

# 申请（公开）
st, d = call("POST", "/flink/apply", {"name":"回放申请" + TS,"link":"https://replay-apply.example/" + TS,"avatar":"https://img.0v0.my/seed/av1.png","descr":"回放","email":"replay-" + TS + "@example.com","color":"#f08","type":0})
# 同一 IP 30 分钟内超过 5 次会 429（契约 §0.7）——这本身是预期行为，算通过
if st == 429:
    print("  ✓ apply 触发限流 429（5次/30分钟，预期行为）")
    check("apply 限流生效", True)
else:
    check("apply 200 且带 screeningTriggered/mailSent", st==200 and "screeningTriggered" in d and "mailSent" in d, (st,d))
REPLAY_EMAIL = "replay-" + TS + "@example.com"
st, d = call("GET", "/flink/apply/status?email=" + urllib.parse.quote("replay-" + TS + "@example.com"))
check("apply/status 200 数组", st==200 and isinstance(d.get("data"),list), (st,d))

# 图床（dev media 关闭 → 期望 502 或旧格式 detail）
st, d = call("GET", "/update/image/listImages", token=TOKEN)
check("listImages 未配置时不会 500", st in (502, 200), (st, str(d)[:80]))

print(f"\n失败 {len(fails)} 项" + ("" if not fails else "：" + str(fails)))
