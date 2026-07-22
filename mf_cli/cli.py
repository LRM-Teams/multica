"""Modelfactory CLI — manage inference services and jobs from the command line.

Usage:
    mf login -u USER -p PWD
    mf create -m /dfs/.../model -g A800-8      # Create a service
    mf list                                     # List services
    mf status SERVICE_ID                        # Check service status
    mf job create -c "sh /path/script.sh" -g A800-8 --gpu-count 2
    mf job list
    mf job status JOB_ID
"""

import argparse
import sys

from .auth import get_token, login as do_login
from .service import create_service, list_services, get_service_status
from .job import create_job, list_jobs, get_job_status
from .config import GPU_SPECS, BASE_URL


def cmd_login(args):
    token = do_login(args.username, args.password, headless=not args.visible)
    print(f"✓ Logged in. Token cached (length={len(token)}).")


# --- Service commands ---

def cmd_create(args):
    result = create_service(
        username=args.username, password=args.password,
        name=args.name, model_path=args.model_path, gpu_label=args.gpu,
        engine=args.engine, engine_version=args.engine_version,
        cpu=args.cpu, memory=args.memory, replicas=args.replicas,
        headless=not args.visible,
    )
    print(f"✓ Service created!")
    print(f"  ID:     {result['service_id']}")
    print(f"  Name:   {result['name']}")
    print(f"  Model:  {result['model_path']}")
    print(f"  GPU:    {result['gpu_label']} × {result['gpu_count']}")
    print(f"  Engine: {result['engine']}")
    print(f"  URL:    {BASE_URL}/home/services_detail/largeService/{result['service_id']}")


def cmd_list(args):
    services = list_services(username=args.username, password=args.password, headless=not args.visible)
    if not services:
        print("No services found.")
        return
    print(f"{'ID':<40} {'Name':<25} {'Status'}")
    print("-" * 100)
    for svc in services:
        sid = svc.id[-30:] if len(svc.id) > 30 else svc.id
        print(f"{sid:<40} {svc.name:<25} {svc.status}")


def cmd_status(args):
    svc = get_service_status(args.service_id, username=args.username, password=args.password, headless=not args.visible)
    if not svc:
        print(f"Service '{args.service_id}' not found.")
        return
    print(f"ID:       {svc.id}")
    print(f"Name:     {svc.name}")
    print(f"Model:    {svc.model_path}")
    print(f"GPU:      {svc.gpu_label} × {svc.gpu_count}")
    print(f"Status:   {svc.status}")


# --- Job commands ---

def cmd_job_create(args):
    command = args.command.split()  # "sh /path/script.sh" -> ["sh", "/path/script.sh"]
    result = create_job(
        username=args.username, password=args.password,
        name=args.name, command=command, image=args.image,
        gpu_label=args.gpu, gpu_count=args.gpu_count,
        cpu=args.cpu, memory_gb=args.memory, instances=args.instances,
        headless=not args.visible,
    )
    print(f"✓ Job created!")
    print(f"  ID:      {result['job_id']}")
    print(f"  Name:    {result['name']}")
    print(f"  Command: {' '.join(result['command'])}")
    print(f"  GPU:     {result['gpu_label']} × {result['gpu_count']}")
    print(f"  CPU:     {result['cpu']} cores,  Memory: {result['memory_gb']} GiB")


def cmd_job_list(args):
    jobs = list_jobs(username=args.username, password=args.password, headless=not args.visible)
    if not jobs:
        print("No jobs found.")
        return
    print(f"{'ID':<35} {'Name':<18} {'GPU':<12} {'Status'}")
    print("-" * 80)
    for j in jobs:
        sid = j.id[-32:] if len(j.id) > 32 else j.id
        gpu = f"{j.gpu_label}×{j.gpu_count}" if j.gpu_label else "-"
        print(f"{sid:<35} {j.name:<18} {gpu:<12} {j.status}")


def cmd_job_status(args):
    job = get_job_status(args.job_id, username=args.username, password=args.password, headless=not args.visible)
    if not job:
        print(f"Job '{args.job_id}' not found.")
        return
    print(f"ID:       {job.id}")
    print(f"Name:     {job.name}")
    print(f"Command:  {' '.join(job.command)}")
    print(f"GPU:      {job.gpu_label} × {job.gpu_count}")
    print(f"Status:   {job.status}")


# --- Main ---

def main():
    parser = argparse.ArgumentParser(description="Modelfactory CLI", prog="mf")
    parser.add_argument("--username", "-u", help="Modelfactory username")
    parser.add_argument("--password", "-p", help="Modelfactory password")
    parser.add_argument("--visible", "-v", action="store_true", help="Show browser during login")

    sub = parser.add_subparsers(dest="command")
    sub.required = True

    # login
    sub.add_parser("login", help="Login and cache auth token")

    # service create
    sc = sub.add_parser("create", help="Create an inference service")
    sc.add_argument("--model-path", "-m", required=True, help="DFS path to model checkpoint")
    sc.add_argument("--name", "-n", default="newService", help="Service name")
    sc.add_argument("--gpu", "-g", default="A800-8", choices=GPU_SPECS, help="GPU spec label")
    sc.add_argument("--engine", default="vllm-openai", help="Inference engine")
    sc.add_argument("--engine-version", default="v0.24.0", help="Engine version")
    sc.add_argument("--cpu", type=int, help="CPU cores (auto)")
    sc.add_argument("--memory", type=int, help="Memory MiB (auto)")
    sc.add_argument("--replicas", type=int, default=1, help="Replicas")

    # service list
    sub.add_parser("list", help="List services")

    # service status
    ss = sub.add_parser("status", help="Check service status")
    ss.add_argument("service_id", help="Service ID")

    # job subcommands
    jp = sub.add_parser("job", help="Job management")
    jsub = jp.add_subparsers(dest="job_cmd")
    jsub.required = True

    jc = jsub.add_parser("create", help="Create a job")
    jc.add_argument("--command", "-c", default="sh", help="Command to run (e.g. 'sh /path/script.sh')")
    jc.add_argument("--name", "-n", default="newjob", help="Job name")
    jc.add_argument("--gpu", "-g", default="A800-8", choices=GPU_SPECS, help="GPU spec label")
    jc.add_argument("--gpu-count", type=int, default=1, help="Number of GPUs")
    jc.add_argument("--cpu", type=int, default=32, help="CPU cores")
    jc.add_argument("--memory", type=int, default=250, help="Memory GiB")
    jc.add_argument("--image", help="Docker workspace image (auto)")
    jc.add_argument("--instances", type=int, default=1, help="Instances")

    jl = jsub.add_parser("list", help="List jobs")
    js = jsub.add_parser("status", help="Check job status")
    js.add_argument("job_id", help="Job ID")

    args = parser.parse_args()

    # Route to handler
    if args.command == "login":
        cmd_login(args)
    elif args.command == "create":
        cmd_create(args)
    elif args.command == "list":
        cmd_list(args)
    elif args.command == "status":
        cmd_status(args)
    elif args.command == "job":
        if args.job_cmd == "create":
            cmd_job_create(args)
        elif args.job_cmd == "list":
            cmd_job_list(args)
        elif args.job_cmd == "status":
            cmd_job_status(args)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
