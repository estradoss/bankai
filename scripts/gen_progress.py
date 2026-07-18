#!/usr/bin/env python3
"""Parse bankai CLAUDE.md roadmap -> progress artifact index.html.
Weights: done=1.0, partial=0.5, todo=0.0. [SKIP] excluded from denominator.
Re-run after every CLAUDE.md edit to refresh the artifact."""
import re, html, sys, datetime

CLAUDE = "/mnt/md0/appdata/claw/home/wkspc/bankai/CLAUDE.md"
OUT = "/mnt/md0/appdata/claw/home/artifacts/-mnt-md0-appdata-claw-home-wkspc-bankai/code/progress/index.html"

txt = open(CLAUDE).read()

# Roadmap items look like:  N. [x] **Name** — ...   (state in [ ], [x], [~], [SKIP])
item_re = re.compile(r'^\s*\d+\.\s*\[([ x~]|SKIP)\]\s*\*\*(.+?)\*\*', re.M)
items = []
for m in item_re.finditer(txt):
    state, name = m.group(1), m.group(2).strip()
    items.append((state, name))

# Phase-1 DONE block: lines "- [x] **Name**" under "### Phase 1 — DONE"
phase1 = []
p1 = re.search(r'### Phase 1 — DONE(.*?)### ', txt, re.S)
if p1:
    for m in re.finditer(r'^\s*-\s*\[([ x~])\]\s*\*\*(.+?)\*\*', p1.group(1), re.M):
        phase1.append((m.group(1), m.group(2).strip()))

W = {' ': 0.0, 'x': 1.0, '~': 0.5}
all_items = phase1 + [(s, n) for (s, n) in items if s != 'SKIP']
done = sum(W.get(s, 0.0) for s, n in all_items)
total = len(all_items)
pct = round(100 * done / total) if total else 0

n_done = sum(1 for s, _ in all_items if s == 'x')
n_part = sum(1 for s, _ in all_items if s == '~')
n_todo = sum(1 for s, _ in all_items if s == ' ')
n_skip = sum(1 for s, _ in items if s == 'SKIP')

def badge(state):
    return {'x': ('done', '✓'), '~': ('partial', '◐'), ' ': ('todo', '○'),
            'SKIP': ('skip', '⊘')}.get(state, ('todo', '○'))

rows = []
for s, n in phase1:
    cls, ico = badge(s)
    rows.append(f'<li class="it {cls}"><span class="ico">{ico}</span><span class="nm">{html.escape(n)}</span><span class="tag">phase 1</span></li>')
for s, n in items:
    cls, ico = badge(s)
    rows.append(f'<li class="it {cls}"><span class="ico">{ico}</span><span class="nm">{html.escape(n)}</span></li>')

now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M")

HTML = f"""<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>bankai — TS→Go parity</title>
<style>
:root{{--bg:#0d1117;--card:#161b22;--bd:#30363d;--tx:#e6edf3;--dim:#8b949e;
--done:#3fb950;--part:#d29922;--todo:#6e7681;--skip:#484f58;--ac:#58a6ff}}
*{{box-sizing:border-box}}
body{{margin:0;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;
background:var(--bg);color:var(--tx);padding:24px}}
.wrap{{max-width:820px;margin:0 auto}}
h1{{font-size:20px;margin:0 0 2px}}
.sub{{color:var(--dim);font-size:12px;margin-bottom:20px}}
.big{{display:flex;align-items:baseline;gap:10px;margin-bottom:8px}}
.pct{{font-size:44px;font-weight:700;color:var(--ac)}}
.frac{{color:var(--dim);font-size:13px}}
.bar{{height:14px;background:var(--card);border:1px solid var(--bd);border-radius:8px;overflow:hidden;display:flex}}
.fill-done{{background:var(--done);width:{100*n_done/total if total else 0:.2f}%}}
.fill-part{{background:var(--part);width:{100*(n_part*0.5)/total if total else 0:.2f}%}}
.legend{{display:flex;gap:16px;flex-wrap:wrap;margin:14px 0 24px;font-size:12px;color:var(--dim)}}
.legend b{{color:var(--tx)}}
.dot{{display:inline-block;width:9px;height:9px;border-radius:50%;margin-right:5px;vertical-align:middle}}
ul{{list-style:none;padding:0;margin:0;display:grid;gap:6px}}
.it{{display:flex;align-items:center;gap:10px;background:var(--card);border:1px solid var(--bd);
border-radius:8px;padding:9px 12px}}
.ico{{font-size:15px;width:16px;text-align:center}}
.nm{{flex:1}}
.tag{{font-size:10px;color:var(--dim);border:1px solid var(--bd);border-radius:10px;padding:1px 8px}}
.done .ico{{color:var(--done)}} .partial .ico{{color:var(--part)}}
.todo .ico{{color:var(--todo)}} .todo .nm{{color:var(--dim)}}
.skip .ico{{color:var(--skip)}} .skip .nm{{color:var(--skip);text-decoration:line-through}}
.foot{{margin-top:20px;color:var(--dim);font-size:11px;text-align:center}}
</style></head><body><div class="wrap">
<h1>bankai — TS→Go parity</h1>
<div class="sub">port of learnvibe (Claude Code fork) — roadmap progress</div>
<div class="big"><span class="pct">{pct}%</span>
<span class="frac">{done:.1f} / {total} weighted units</span></div>
<div class="bar"><div class="fill-done"></div><div class="fill-part"></div></div>
<div class="legend">
<span><span class="dot" style="background:var(--done)"></span><b>{n_done}</b> done</span>
<span><span class="dot" style="background:var(--part)"></span><b>{n_part}</b> partial</span>
<span><span class="dot" style="background:var(--todo)"></span><b>{n_todo}</b> todo</span>
<span><span class="dot" style="background:var(--skip)"></span><b>{n_skip}</b> skipped</span>
</div>
<ul>{''.join(rows)}</ul>
<div class="foot">generated {now} from CLAUDE.md</div>
</div></body></html>"""

open(OUT, "w").write(HTML)
print(f"pct={pct}% done={n_done} part={n_part} todo={n_todo} skip={n_skip} -> {OUT}")
