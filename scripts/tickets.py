#!/usr/bin/env python3
"""Ticket and issue overview, and the next task to take (AGENTS.md, "Taking the next task").

    scripts/tickets.py          table of all tickets, then of agent-ready GitHub issues
    scripts/tickets.py --open   the same, without done tickets
    scripts/tickets.py --next   only the next task: "<NNN> new", "<NNN> resume <path>",
                                "issue <N> new" or "issue <N> resume <path>";
                                exit 1 if nothing is ready
    scripts/tickets.py --closable   open issues whose tickets (frontmatter `issues: [N]`) are all
                                    done: "<N> <ticket> <ticket> ..."; they can be closed

GitHub issues count as tasks only when they carry the `agent-ready` label and not
`needs-decision`. Only maintainers can set labels, so nobody else can queue work. An issue that a
ticket names in its frontmatter (`issues: [N]`) is planned: its work is those tickets, so it is
not a task of its own.

Ticket status comes from origin/main. Work in progress comes from open PRs, local worktrees and
their `.owner` PID, and `ticket/*` branches on origin.

States, in the order AGENTS.md cares about them:
  done       status: done on main
  review     an open PR exists
  working    a worktree whose .owner process is alive
  abandoned  a worktree with no live owner, or a ticket branch with no worktree and no PR
  ready      todo, every dependency done, nothing in progress
  blocked    todo, waiting for the listed dependencies
"""

import json
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(
    subprocess.run(
        ["git", "-C", str(Path(__file__).parent), "rev-parse", "--show-toplevel"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
)


def run(*args: str) -> str:
    return subprocess.run(args, cwd=ROOT, capture_output=True, text=True, check=True).stdout


def tickets_on_main() -> dict[str, dict]:
    tickets = {}
    for path in run("git", "ls-tree", "--name-only", "origin/main", "tickets/").split():
        m = re.match(r"tickets/(\d{3}[a-z]?)-.*\.md$", path)
        if not m:
            continue
        text = run("git", "show", f"origin/main:{path}")
        front = text.split("---")[1] if text.startswith("---") else ""
        status = re.search(r"^status:\s*(\S+)", front, re.M)
        deps = re.search(r"^depends_on:\s*\[(.*)\]", front, re.M)
        refs = re.search(r"^issues:\s*\[(.*)\]", front, re.M)
        title = re.search(r"^# (.+)$", text, re.M)
        tickets[m[1]] = {
            "status": status[1] if status else "?",
            "deps": [d.strip() for d in deps[1].split(",") if d.strip()] if deps else [],
            "title": title[1] if title else path,
            "issues": [i.strip().lstrip("#") for i in refs[1].split(",") if i.strip()] if refs else [],
        }
    return tickets


def ticket_of(branch: str) -> str | None:
    m = re.match(r"(?:refs/heads/)?ticket/(\d{3}[a-z]?)-", branch)
    return m[1] if m else None


def issue_of(branch: str) -> str | None:
    m = re.match(r"(?:refs/heads/)?issue/(\d+)-", branch)
    return "issue " + m[1] if m else None


def task_of(branch: str) -> str | None:
    return ticket_of(branch) or issue_of(branch)


def agent_ready_issues(planned: set[str]) -> dict[str, dict]:
    issues = json.loads(run("gh", "issue", "list", "--state", "open", "--label", "agent-ready",
                            "--limit", "100", "--json", "number,title,labels"))
    return {
        f"issue {i['number']}": {"title": i["title"], "status": "todo", "deps": [], "issues": []}
        for i in sorted(issues, key=lambda i: i["number"])
        if "needs-decision" not in {l["name"] for l in i["labels"]}
        and str(i["number"]) not in planned
    }


def open_prs() -> dict[str, int]:
    prs = json.loads(run("gh", "pr", "list", "--state", "open", "--json", "number,headRefName"))
    return {t: p["number"] for p in prs if (t := task_of(p["headRefName"]))}


def worktrees() -> dict[str, dict]:
    result = {}
    for block in run("git", "worktree", "list", "--porcelain").strip().split("\n\n"):
        fields = dict(line.split(" ", 1) for line in block.splitlines() if " " in line)
        t = task_of(fields.get("branch", ""))
        if not t:
            continue
        path = Path(fields["worktree"])
        owner = None
        try:
            pid = int((path / ".owner").read_text().strip())
            os.kill(pid, 0)
            owner = pid
        except (OSError, ValueError):
            pass
        result[t] = {"path": os.path.relpath(path, ROOT), "owner": owner}
    return result


def remote_branches() -> set[str]:
    out = run("git", "for-each-ref", "--format=%(refname:short)",
              "refs/remotes/origin/ticket/", "refs/remotes/origin/issue/")
    return {t for b in out.split() if (t := task_of(b.removeprefix("origin/")))}


def closable(tickets: dict[str, dict]) -> int:
    by_issue: dict[str, list[str]] = {}
    for num, t in tickets.items():
        for i in t["issues"]:
            by_issue.setdefault(i, []).append(num)
    open_issues = {str(i["number"]) for i in json.loads(
        run("gh", "issue", "list", "--state", "open", "--limit", "500", "--json", "number"))}
    for i, nums in sorted(by_issue.items(), key=lambda kv: int(kv[0])):
        if i in open_issues and all(tickets[n]["status"] == "done" for n in nums):
            print(i, *nums)
    return 0


def main() -> int:
    args = set(sys.argv[1:])
    run("git", "fetch", "-q", "--prune", "origin")
    tickets = tickets_on_main()
    if "--closable" in args:
        return closable(tickets)
    issues = agent_ready_issues({i for t in tickets.values() for i in t["issues"]})
    tickets.update(issues)
    prs, trees, branches = open_prs(), worktrees(), remote_branches()

    for num, t in tickets.items():
        missing = [d for d in t["deps"] if tickets.get(d, {}).get("status") != "done"]
        tree = trees.get(num)
        if t["status"] == "done":
            state, note = "done", ""
        elif num in prs:
            state, note = "review", f"PR #{prs[num]}"
        elif tree and tree["owner"]:
            state, note = "working", f"{tree['path']} (pid {tree['owner']})"
        elif tree:
            state, note = "abandoned", f"{tree['path']}, no live owner"
        elif num in branches:
            prefix = "issue/" + num.split()[1] if num in issues else "ticket/" + num
            state, note = "abandoned", f"branch origin/{prefix}-*, no PR"
        elif missing:
            state, note = "blocked", "waits for " + ", ".join(missing)
        else:
            state, note = "ready", ""
        t.update(state=state, note=note, missing=missing)

    # AGENTS.md: resume abandoned work first, then the lowest-numbered ready ticket, then the
    # lowest-numbered ready issue (tickets come first in `tickets`, issues after them).
    nxt = next((n for n, t in tickets.items() if t["state"] == "abandoned"), None)
    nxt = nxt or next((n for n, t in tickets.items() if t["state"] == "ready"), None)

    if "--next" in args:
        if not nxt:
            return 1
        t = tickets[nxt]
        branch = ("issue/" + nxt.split()[1] if nxt in issues else "ticket/" + nxt) + "-*"
        print(f"{nxt} resume {trees[nxt]['path']}" if t["state"] == "abandoned" and nxt in trees
              else f"{nxt} resume origin/{branch}" if t["state"] == "abandoned"
              else f"{nxt} new")
        return 0

    def dep_list(t: dict) -> str:
        return " ".join(d + ("" if d not in t["missing"] else "*") for d in t["deps"])

    rows = [(n, t) for n, t in tickets.items() if not ("--open" in args and t["state"] == "done")]
    if not rows:
        print("no open tickets" if "--open" in args else "no tickets")
    else:
        width = min(max(len(t["title"]) for _, t in rows), 46)
        print(f"{'#':<9} {'TITLE':<{width}}  {'STATE':<9}  {'DEPENDS ON':<14}  NOTE")
    for n, t in rows:
        if n in issues and n == next(iter(issues)):
            print("\nagent-ready GitHub issues:")
        title = t["title"] if len(t["title"]) <= width else t["title"][: width - 1] + "…"
        print(f"{n:<9} {title:<{width}}  {t['state']:<9}  {dep_list(t):<14}  {t['note']}")
    counts = {}
    for t in tickets.values():
        counts[t["state"]] = counts.get(t["state"], 0) + 1
    print("\n" + ", ".join(f"{v} {k}" for k, v in counts.items()) + "   (* = dependency not done)")
    print(f"next: {nxt} ({tickets[nxt]['state']})" if nxt else "next: none ready")
    return 0


if __name__ == "__main__":
    sys.exit(main())
