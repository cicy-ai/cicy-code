#!/usr/bin/env python3
"""cping - 国内多节点 Ping 测试，爬取 itdog.cn 数据"""
import re, sys, subprocess, requests

def fetch_itdog(target):
    """从 itdog.cn 抓取 ping 数据"""
    resp = requests.get(
        f"https://www.itdog.cn/ping/{target}",
        headers={"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
        timeout=15
    )
    if resp.status_code != 200:
        return None

    html = resp.text
    # 解析每个节点行
    rows = re.findall(
        r'<tr class="node_tr"[^>]*>(.*?)</tr>',
        html, re.DOTALL
    )

    results = []
    for row in rows:
        # 运营商 + 地区
        isp_m = re.search(r'badge[^"]*">(.*?)</span>\s*(.*?)\s*</td>', row, re.DOTALL)
        if not isp_m:
            continue
        isp = isp_m.group(1).strip()
        location = isp_m.group(2).strip()

        # 平均延迟
        avg_m = re.search(r'id="avg_ping_\d+"[^>]*>([^<]+)', row)
        avg = avg_m.group(1).strip() if avg_m else "--"

        # 丢包率
        loss_m = re.search(r'id="loss_\d+"[^>]*>([^<]+)', row)
        loss = loss_m.group(1).strip() if loss_m else ""

        results.append({"isp": isp, "location": location, "avg": avg, "loss": loss})

    return results

def resolve(target):
    try:
        r = subprocess.run(["dig", "+short", target], capture_output=True, text=True, timeout=5)
        ip = r.stdout.strip().split("\n")[0]
        return ip if ip else target
    except:
        return target

def main():
    if len(sys.argv) < 2:
        print("用法: cping <domain_or_ip>")
        sys.exit(1)

    target = sys.argv[1]
    ip = resolve(target)

    print(f"\n🏓 cping - {target} ({ip})")
    print("━" * 50)

    results = fetch_itdog(target)
    if not results:
        print("  ❌ 无法获取数据")
        print(f"  请浏览器打开: https://www.itdog.cn/ping/{target}")
        sys.exit(1)

    # 按运营商分组
    groups = {}
    for r in results:
        groups.setdefault(r["isp"], []).append(r)

    for isp in ["电信", "联通", "移动", "海外"]:
        nodes = groups.get(isp, [])
        if not nodes:
            continue
        print(f"\n  📡 {isp}:")
        for n in nodes:
            avg = n["avg"]
            loc = n["location"]
            loss = n["loss"]
            if avg == "--":
                print(f"    {loc:<14} ❌ 超时")
            else:
                extra = f" (丢包{loss})" if loss and loss != "0%" else ""
                print(f"    {loc:<14} ✅ {avg}ms{extra}")

    # 统计
    valid = [int(r["avg"]) for r in results if r["avg"] != "--" and r["avg"].isdigit()]
    if valid:
        print(f"\n  📊 最快: {min(valid)}ms | 最慢: {max(valid)}ms | 平均: {round(sum(valid)/len(valid))}ms")
        print(f"  📊 节点: {len(results)}个 | 可达: {len(valid)}个 | 超时: {len(results)-len(valid)}个")

if __name__ == "__main__":
    main()
