"""Modelfactory CLI — manage services, jobs, and workspaces."""

import argparse
import sys

from .auth import get_token, login as do_login
from .service import create_service, list_services, get_service_status
from .job import create_job, list_jobs, get_job_status
from .workspace import (create_workspace, list_workspaces, get_workspace,
                        restart_workspace, stop_workspace, save_workspace, delete_workspace)
from .config import GPU_SPECS, BASE_URL


def cmd_login(args):
    token = do_login(args.username, args.password, headless=not args.visible)
    print(f"✓ Logged in. Token cached (length={len(token)}).")

# --- Service ---

def cmd_create(args):
    r = create_service(username=args.username, password=args.password, name=args.name,
        model_path=args.model_path, gpu_label=args.gpu, engine=args.engine,
        engine_version=args.engine_version, cpu=args.cpu, memory=args.memory,
        replicas=args.replicas, headless=not args.visible)
    print(f"✓ Service created!  ID: {r['service_id']}  Name: {r['name']}  GPU: {r['gpu_label']}×{r['gpu_count']}  URL: {BASE_URL}/home/services_detail/largeService/{r['service_id']}")

def cmd_list(args):
    svcs = list_services(username=args.username, password=args.password, headless=not args.visible)
    if not svcs: print("No services."); return
    print(f"{'ID':<40} {'Name':<25} {'Status'}\n{'-'*100}")
    for s in svcs: print(f"{s.id[-30:]:<40} {s.name:<25} {s.status}")

def cmd_status(args):
    s = get_service_status(args.service_id, username=args.username, password=args.password, headless=not args.visible)
    if not s: print(f"Not found: {args.service_id}"); return
    print(f"ID: {s.id}\nName: {s.name}\nModel: {s.model_path}\nGPU: {s.gpu_label}×{s.gpu_count}\nStatus: {s.status}")

# --- Job ---

def cmd_job_create(args):
    r = create_job(username=args.username, password=args.password, name=args.name,
        command=args.command.split(), image=args.image, gpu_label=args.gpu,
        gpu_count=args.gpu_count, cpu=args.cpu, memory_gb=args.memory,
        instances=args.instances, headless=not args.visible)
    print(f"✓ Job created!  ID: {r['job_id']}  Name: {r['name']}  GPU: {r['gpu_label']}×{r['gpu_count']}")

def cmd_job_list(args):
    jobs = list_jobs(username=args.username, password=args.password, headless=not args.visible)
    if not jobs: print("No jobs."); return
    print(f"{'ID':<35} {'Name':<18} {'GPU':<12} {'Status'}\n{'-'*80}")
    for j in jobs: print(f"{j.id[-32:]:<35} {j.name:<18} {j.gpu_label}×{j.gpu_count:<5} {j.status}")

def cmd_job_status(args):
    j = get_job_status(args.job_id, username=args.username, password=args.password, headless=not args.visible)
    if not j: print(f"Not found: {args.job_id}"); return
    print(f"ID: {j.id}\nName: {j.name}\nCommand: {' '.join(j.command)}\nGPU: {j.gpu_label}×{j.gpu_count}\nStatus: {j.status}")

# --- Workspace ---

def cmd_ws_create(args):
    r = create_workspace(username=args.username, password=args.password, name=args.name,
        gpu_label=args.gpu, gpu_count=args.gpu_count, cpu=args.cpu,
        memory_gb=args.memory, image=args.image, ssh_enabled=args.ssh,
        headless=not args.visible)
    print(f"✓ Workspace created!  ID: {r['workspace_id']}  Name: {r['name']}  GPU: {r['gpu_label']}×{r['gpu_count']}  Image: {r['image']}")

def cmd_ws_list(args):
    wss = list_workspaces(username=args.username, password=args.password, headless=not args.visible)
    if not wss: print("No workspaces."); return
    print(f"{'ID':<38} {'Name':<18} {'GPU':<12} {'Status'}\n{'-'*90}")
    for w in wss: print(f"{w.id[-36:]:<38} {w.name:<18} {w.gpu_label}×{w.gpu_count:<5} {w.status}")

def cmd_ws_action(args):
    action = args.ws_action
    fn = {"restart": restart_workspace, "stop": stop_workspace,
          "save": save_workspace, "delete": delete_workspace}[action]
    r = fn(args.workspace_id, username=args.username, password=args.password, headless=not args.visible)
    print(f"✓ Workspace {action}d: {r.get('workspace_id', args.workspace_id)}")

def cmd_ws_status(args):
    w = get_workspace(args.workspace_id, username=args.username, password=args.password, headless=not args.visible)
    if not w: print(f"Not found: {args.workspace_id}"); return
    print(f"ID: {w.id}\nName: {w.name}\nImage: {w.image}\nGPU: {w.gpu_label}×{w.gpu_count}\nCPU: {w.cpu} cores\nMem: {w.memory_mb} MiB\nStatus: {w.status}")


def main():
    p = argparse.ArgumentParser(description="Modelfactory CLI", prog="mf")
    p.add_argument("--username", "-u"); p.add_argument("--password", "-p")
    p.add_argument("--visible", "-v", action="store_true", help="Show browser")

    sub = p.add_subparsers(dest="command"); sub.required = True
    sub.add_parser("login")

    sc = sub.add_parser("create", help="Create service")
    sc.add_argument("--model-path", "-m", required=True); sc.add_argument("--name", "-n", default="newService")
    sc.add_argument("--gpu", "-g", default="A800-8", choices=GPU_SPECS); sc.add_argument("--engine", default="vllm-openai")
    sc.add_argument("--engine-version", default="v0.24.0"); sc.add_argument("--cpu", type=int)
    sc.add_argument("--memory", type=int); sc.add_argument("--replicas", type=int, default=1)

    sub.add_parser("list", help="List services")
    ss = sub.add_parser("status", help="Service status"); ss.add_argument("service_id")

    # Job
    jp = sub.add_parser("job", help="Jobs"); js = jp.add_subparsers(dest="job_cmd"); js.required = True
    jc = js.add_parser("create"); jc.add_argument("--command", "-c", default="sh"); jc.add_argument("--name", "-n", default="newjob")
    jc.add_argument("--gpu", "-g", default="A800-8", choices=GPU_SPECS); jc.add_argument("--gpu-count", type=int, default=1)
    jc.add_argument("--cpu", type=int, default=32); jc.add_argument("--memory", type=int, default=250)
    jc.add_argument("--image"); jc.add_argument("--instances", type=int, default=1)
    js.add_parser("list")
    js2 = js.add_parser("status"); js2.add_argument("job_id")

    # Workspace
    wp = sub.add_parser("ws", help="Workspaces"); ws = wp.add_subparsers(dest="ws_cmd"); ws.required = True
    wc = ws.add_parser("create", help="Create workspace")
    wc.add_argument("--name", "-n", default="newWorkspace"); wc.add_argument("--gpu", "-g", default="A800-8", choices=GPU_SPECS)
    wc.add_argument("--gpu-count", type=int, default=1); wc.add_argument("--cpu", type=int, default=8)
    wc.add_argument("--memory", type=int, default=80, help="Memory GiB")
    wc.add_argument("--image", help="Docker image (default: pytorch 2.8.0)")
    wc.add_argument("--ssh", action="store_true", help="Enable SSH")
    ws.add_parser("list", help="List workspaces")
    wa = ws.add_parser("restart"); wa.add_argument("workspace_id"); wa.add_argument("--action", dest="ws_action", default="restart", help=argparse.SUPPRESS)
    wa = ws.add_parser("stop"); wa.add_argument("workspace_id"); wa.add_argument("--action", dest="ws_action", default="stop", help=argparse.SUPPRESS)
    wa = ws.add_parser("save"); wa.add_argument("workspace_id"); wa.add_argument("--action", dest="ws_action", default="save", help=argparse.SUPPRESS)
    wa = ws.add_parser("delete"); wa.add_argument("workspace_id"); wa.add_argument("--action", dest="ws_action", default="delete", help=argparse.SUPPRESS)
    wst = ws.add_parser("status"); wst.add_argument("workspace_id")

    args = p.parse_args()

    if args.command == "login": cmd_login(args)
    elif args.command == "create": cmd_create(args)
    elif args.command == "list": cmd_list(args)
    elif args.command == "status": cmd_status(args)
    elif args.command == "job":
        if args.job_cmd == "create": cmd_job_create(args)
        elif args.job_cmd == "list": cmd_job_list(args)
        elif args.job_cmd == "status": cmd_job_status(args)
    elif args.command == "ws":
        if args.ws_cmd == "create": cmd_ws_create(args)
        elif args.ws_cmd == "list": cmd_ws_list(args)
        elif args.ws_cmd == "status": cmd_ws_status(args)
        elif args.ws_cmd in ("restart", "stop", "save", "delete"): cmd_ws_action(args)

if __name__ == "__main__": main()
