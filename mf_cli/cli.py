"""Modelfactory CLI — manage services, jobs, and workspaces."""

import argparse

from .auth import login as do_login
from .config import BASE_URL, GPU_SPECS
from .job import create_job, get_job_status, list_jobs
from .register import (
    _load_env,
    edit_model_in_leagent,
    get_service_endpoint,
    register_model_in_leagent,
)
from .service import create_service, get_service_status, list_services
from .workspace import (
    create_workspace,
    delete_workspace,
    get_workspace,
    list_workspaces,
    restart_workspace,
    save_workspace,
    stop_workspace,
)


def cmd_login(args):
    token = do_login(args.username, args.password, headless=not args.visible)
    print(f"✓ Logged in. Token cached (length={len(token)}).")


# --- Service ---


def cmd_create(args):
    r = create_service(
        username=args.username,
        password=args.password,
        name=args.name,
        model_path=args.model_path,
        gpu_label=args.gpu,
        engine=args.engine,
        engine_version=args.engine_version,
        cpu=args.cpu,
        memory=args.memory,
        replicas=args.replicas,
        headless=not args.visible,
    )
    print(
        f"✓ Service created!  ID: {r['service_id']}  Name: {r['name']}  GPU: {r['gpu_label']}×{r['gpu_count']}  URL: {BASE_URL}/home/services_detail/largeService/{r['service_id']}"
    )


def cmd_list(args):
    svcs = list_services(
        username=args.username, password=args.password, headless=not args.visible
    )
    if not svcs:
        print("No services.")
        return
    print(f"{'ID':<40} {'Name':<25} {'Status'}\n{'-' * 100}")
    for s in svcs:
        print(f"{s.id[-30:]:<40} {s.name:<25} {s.status}")


def cmd_status(args):
    s = get_service_status(
        args.service_id,
        username=args.username,
        password=args.password,
        headless=not args.visible,
    )
    if not s:
        print(f"Not found: {args.service_id}")
        return
    print(
        f"ID: {s.id}\nName: {s.name}\nModel: {s.model_path}\nGPU: {s.gpu_label}×{s.gpu_count}\nStatus: {s.status}"
    )


# --- Job ---


def cmd_job_create(args):
    r = create_job(
        username=args.username,
        password=args.password,
        name=args.name,
        command=args.command.split(),
        image=args.image,
        gpu_label=args.gpu,
        gpu_count=args.gpu_count,
        cpu=args.cpu,
        memory_gb=args.memory,
        instances=args.instances,
        headless=not args.visible,
    )
    print(
        f"✓ Job created!  ID: {r['job_id']}  Name: {r['name']}  GPU: {r['gpu_label']}×{r['gpu_count']}"
    )


def cmd_job_list(args):
    jobs = list_jobs(
        username=args.username, password=args.password, headless=not args.visible
    )
    if not jobs:
        print("No jobs.")
        return
    print(f"{'ID':<35} {'Name':<18} {'GPU':<12} {'Status'}\n{'-' * 80}")
    for j in jobs:
        print(
            f"{j.id[-32:]:<35} {j.name:<18} {j.gpu_label}×{j.gpu_count:<5} {j.status}"
        )


def cmd_job_status(args):
    j = get_job_status(
        args.job_id,
        username=args.username,
        password=args.password,
        headless=not args.visible,
    )
    if not j:
        print(f"Not found: {args.job_id}")
        return
    print(
        f"ID: {j.id}\nName: {j.name}\nCommand: {' '.join(j.command)}\nGPU: {j.gpu_label}×{j.gpu_count}\nStatus: {j.status}"
    )


# --- Workspace ---


def cmd_ws_create(args):
    r = create_workspace(
        username=args.username,
        password=args.password,
        name=args.name,
        gpu_label=args.gpu,
        gpu_count=args.gpu_count,
        cpu=args.cpu,
        memory_gb=args.memory,
        image=args.image,
        ssh_enabled=args.ssh,
        headless=not args.visible,
    )
    print(
        f"✓ Workspace created!  ID: {r['workspace_id']}  Name: {r['name']}  GPU: {r['gpu_label']}×{r['gpu_count']}  Image: {r['image']}"
    )


def cmd_ws_list(args):
    wss = list_workspaces(
        username=args.username, password=args.password, headless=not args.visible
    )
    if not wss:
        print("No workspaces.")
        return
    print(f"{'ID':<38} {'Name':<18} {'GPU':<12} {'Status'}\n{'-' * 90}")
    for w in wss:
        print(
            f"{w.id[-36:]:<38} {w.name:<18} {w.gpu_label}×{w.gpu_count:<5} {w.status}"
        )


def cmd_ws_action(args):
    action = args.ws_action
    fn = {
        "restart": restart_workspace,
        "stop": stop_workspace,
        "save": save_workspace,
        "delete": delete_workspace,
    }[action]
    r = fn(
        args.workspace_id,
        username=args.username,
        password=args.password,
        headless=not args.visible,
    )
    print(f"✓ Workspace {action}d: {r.get('workspace_id', args.workspace_id)}")


def cmd_ws_status(args):
    w = get_workspace(
        args.workspace_id,
        username=args.username,
        password=args.password,
        headless=not args.visible,
    )
    if not w:
        print(f"Not found: {args.workspace_id}")
        return
    print(
        f"ID: {w.id}\nName: {w.name}\nImage: {w.image}\nGPU: {w.gpu_label}×{w.gpu_count}\nCPU: {w.cpu} cores\nMem: {w.memory_mb} MiB\nStatus: {w.status}"
    )


# --- Register (Leagent integration) ---


def cmd_endpoint(args):
    """Print the inference endpoint URL and API key for a Modelfactory service."""
    ep = get_service_endpoint(
        args.service_id,
        username=args.username,
        password=args.password,
        headless=not args.visible,
    )
    print(f"Service ID:    {ep.service_id}")
    print(f"Service Name:  {ep.service_name}")
    print(f"Model Name:    {ep.model_name}")
    print(f"Model Path:    {ep.model_path}")
    print(f"Status:        {ep.status}")
    print(f"Endpoint URL:  {ep.endpoint_url}")
    print(f"API Key:       {ep.api_key}")


def cmd_register(args):
    """Get service endpoint and register it as a model in Leagent."""
    env = _load_env()
    ep = get_service_endpoint(
        args.service_id,
        username=args.username,
        password=args.password,
        headless=not args.visible,
    )
    print(f"Service:  {ep.service_name} ({ep.service_id})")
    print(f"Model:    {ep.model_name}")
    print(f"Endpoint: {ep.endpoint_url}")
    print(f"Status:   {ep.status}")
    print()

    # Resolve Leagent connection params with .env fallback
    leagent_url = args.leagent_url or env.get("LEAGENT_URL", "")
    supabase_url = args.supabase_url or env.get("SUPABASE_URL", "")
    anon_key = args.anon_key or env.get("SUPABASE_ANON_KEY", "")
    admin_email = args.admin_email or env.get("LEAGENT_ADMIN_EMAIL", "")
    admin_password = args.admin_password or env.get("LEAGENT_ADMIN_PASSWORD", "")

    if args.dry_run:
        print("[dry-run] Would register with:")
        print(f"  Leagent URL: {leagent_url}")
        print(f"  Model name:  {ep.model_name}")
        print(f"  Provider:    {args.provider}")
        print(f"  API base:    {ep.endpoint_url}")
        return

    print(f"Registering in Leagent at {leagent_url} ...")
    try:
        result = register_model_in_leagent(
            ep,
            leagent_url=leagent_url,
            supabase_url=supabase_url,
            anon_key=anon_key,
            admin_email=admin_email,
            admin_password=admin_password,
            provider=args.provider,
            display_name=args.display_name or None,
            max_output_tokens=args.max_output_tokens,
            context_window=args.context_window,
            capabilities=args.capabilities.split(",") if args.capabilities else None,
            description=args.description or None,
        )
        print(f"✓ Model registered!  ID: {result.get('id', 'unknown')}")
    except Exception as e:
        print(f"✖ Registration failed: {e}")
        raise SystemExit(1)


def cmd_edit(args):
    """Edit an existing model's URL and/or API key in Leagent."""
    env = _load_env()

    # Resolve Leagent connection params with .env fallback
    leagent_url = args.leagent_url or env.get("LEAGENT_URL", "")
    supabase_url = args.supabase_url or env.get("SUPABASE_URL", "")
    anon_key = args.anon_key or env.get("SUPABASE_ANON_KEY", "")
    admin_email = args.admin_email or env.get("LEAGENT_ADMIN_EMAIL", "")
    admin_password = args.admin_password or env.get("LEAGENT_ADMIN_PASSWORD", "")

    if args.dry_run:
        print(f"[dry-run] Would edit model '{args.model}' with:")
        if args.url:
            print(f"  New URL: {args.url}")
        if args.key:
            print(f"  New key: {'*' * min(len(args.key), 8)}...")
        print(f"  Leagent URL: {leagent_url}")
        return

    print(f"Editing model '{args.model}' in Leagent at {leagent_url} ...")
    try:
        result = edit_model_in_leagent(
            model_identifier=args.model,
            leagent_url=leagent_url,
            supabase_url=supabase_url,
            anon_key=anon_key,
            admin_email=admin_email,
            admin_password=admin_password,
            api_base=args.url or None,
            api_key=args.key or None,
        )
        model_id = result.get("id", "unknown")
        model_name = result.get("model_name", "unknown")
        print(f"✓ Model updated!  ID: {model_id}  Name: {model_name}")
    except Exception as e:
        print(f"✖ Edit failed: {e}")
        raise SystemExit(1)


def cmd_delete(args):
    """Delete a model from Leagent by name or ID."""
    env = _load_env()

    leagent_url = args.leagent_url or env.get("LEAGENT_URL", "")
    supabase_url = args.supabase_url or env.get("SUPABASE_URL", "")
    anon_key = args.anon_key or env.get("SUPABASE_ANON_KEY", "")
    admin_email = args.admin_email or env.get("LEAGENT_ADMIN_EMAIL", "")
    admin_password = args.admin_password or env.get("LEAGENT_ADMIN_PASSWORD", "")

    if args.dry_run:
        print(
            f"[dry-run] Would delete model '{args.model}' from Leagent at {leagent_url}"
        )
        return

    print(f"Deleting model '{args.model}' from Leagent at {leagent_url} ...")
    try:
        result = delete_model_in_leagent(
            model_identifier=args.model,
            leagent_url=leagent_url,
            supabase_url=supabase_url,
            anon_key=anon_key,
            admin_email=admin_email,
            admin_password=admin_password,
        )
        print(f"✓ Model deleted!  ID: {result['id']}  Name: {result['model_name']}")
    except Exception as e:
        print(f"✖ Delete failed: {e}")
        raise SystemExit(1)


def main():
    p = argparse.ArgumentParser(description="Modelfactory CLI", prog="mf")
    p.add_argument("--username", "-u")
    p.add_argument("--password", "-p")
    p.add_argument("--visible", "-v", action="store_true", help="Show browser")

    sub = p.add_subparsers(dest="command")
    sub.required = True
    sub.add_parser("login")

    sc = sub.add_parser("create", help="Create service")
    sc.add_argument("--model-path", "-m", required=True)
    sc.add_argument("--name", "-n", default="newService")
    sc.add_argument("--gpu", "-g", default="A800-8", choices=GPU_SPECS)
    sc.add_argument("--engine", default="vllm-openai")
    sc.add_argument("--engine-version", default="v0.24.0")
    sc.add_argument("--cpu", type=int)
    sc.add_argument("--memory", type=int)
    sc.add_argument("--replicas", type=int, default=1)

    sub.add_parser("list", help="List services")
    ss = sub.add_parser("status", help="Service status")
    ss.add_argument("service_id")

    # Job
    jp = sub.add_parser("job", help="Jobs")
    js = jp.add_subparsers(dest="job_cmd")
    js.required = True
    jc = js.add_parser("create")
    jc.add_argument("--command", "-c", default="sh")
    jc.add_argument("--name", "-n", default="newjob")
    jc.add_argument("--gpu", "-g", default="A800-8", choices=GPU_SPECS)
    jc.add_argument("--gpu-count", type=int, default=1)
    jc.add_argument("--cpu", type=int, default=32)
    jc.add_argument("--memory", type=int, default=250)
    jc.add_argument("--image")
    jc.add_argument("--instances", type=int, default=1)
    js.add_parser("list")
    js2 = js.add_parser("status")
    js2.add_argument("job_id")

    # Workspace
    wp = sub.add_parser("ws", help="Workspaces")
    ws = wp.add_subparsers(dest="ws_cmd")
    ws.required = True
    wc = ws.add_parser("create", help="Create workspace")
    wc.add_argument("--name", "-n", default="newWorkspace")
    wc.add_argument("--gpu", "-g", default="A800-8", choices=GPU_SPECS)
    wc.add_argument("--gpu-count", type=int, default=1)
    wc.add_argument("--cpu", type=int, default=8)
    wc.add_argument("--memory", type=int, default=80, help="Memory GiB")
    wc.add_argument("--image", help="Docker image (default: pytorch 2.8.0)")
    wc.add_argument("--ssh", action="store_true", help="Enable SSH")
    ws.add_parser("list", help="List workspaces")
    wa = ws.add_parser("restart")
    wa.add_argument("workspace_id")
    wa.add_argument(
        "--action", dest="ws_action", default="restart", help=argparse.SUPPRESS
    )
    wa = ws.add_parser("stop")
    wa.add_argument("workspace_id")
    wa.add_argument(
        "--action", dest="ws_action", default="stop", help=argparse.SUPPRESS
    )
    wa = ws.add_parser("save")
    wa.add_argument("workspace_id")
    wa.add_argument(
        "--action", dest="ws_action", default="save", help=argparse.SUPPRESS
    )
    wa = ws.add_parser("delete")
    wa.add_argument("workspace_id")
    wa.add_argument(
        "--action", dest="ws_action", default="delete", help=argparse.SUPPRESS
    )
    wst = ws.add_parser("status")
    wst.add_argument("workspace_id")

    # Endpoint — show service inference URL and API key
    ep = sub.add_parser(
        "endpoint", help="Show service inference endpoint (URL + API key)"
    )
    ep.add_argument("service_id")

    # Register — register a Modelfactory service as a Leagent model
    reg = sub.add_parser(
        "register", help="Register a Modelfactory service as a Leagent model"
    )
    reg.add_argument("service_id")
    reg.add_argument(
        "--leagent-url",
        default="http://10.110.158.146:8000",
        help="Leagent backend URL (default: http://10.110.158.146:8000)",
    )
    reg.add_argument(
        "--supabase-url",
        default=None,
        help="Supabase URL for Leagent auth (default: from .env SUPABASE_URL)",
    )
    reg.add_argument(
        "--anon-key",
        default=None,
        help="Supabase anon key (default: from .env SUPABASE_ANON_KEY)",
    )
    reg.add_argument(
        "--admin-email",
        default=None,
        help="Leagent admin email (default: from .env LEAGENT_ADMIN_EMAIL)",
    )
    reg.add_argument(
        "--admin-password",
        default=None,
        help="Leagent admin password (default: from .env LEAGENT_ADMIN_PASSWORD)",
    )
    reg.add_argument(
        "--provider", default="openai-compatible", help="Model provider identifier"
    )
    reg.add_argument("--display-name", help="Human-readable display name")
    reg.add_argument("--max-output-tokens", type=int, default=16384)
    reg.add_argument("--context-window", type=int, default=131072)
    reg.add_argument("--capabilities", help="Comma-separated capabilities")
    reg.add_argument("--description", help="Model description")
    reg.add_argument(
        "--dry-run",
        action="store_true",
        help="Show what would be registered without doing it",
    )

    # Edit — update an existing model's URL and/or key in Leagent
    ed = sub.add_parser("edit", help="Edit a Leagent model's URL and/or API key")
    ed.add_argument("model", help="Model name or ID to edit")
    ed.add_argument("--url", help="New inference endpoint URL")
    ed.add_argument("--key", "-k", help="New API key")
    ed.add_argument(
        "--leagent-url",
        default="http://10.110.158.146:8000",
        help="Leagent backend URL (default: http://10.110.158.146:8000)",
    )
    ed.add_argument(
        "--supabase-url",
        default=None,
        help="Supabase URL for Leagent auth (default: from .env SUPABASE_URL)",
    )
    ed.add_argument(
        "--anon-key",
        default=None,
        help="Supabase anon key (default: from .env SUPABASE_ANON_KEY)",
    )
    ed.add_argument(
        "--admin-email",
        default=None,
        help="Leagent admin email (default: from .env LEAGENT_ADMIN_EMAIL)",
    )
    ed.add_argument(
        "--admin-password",
        default=None,
        help="Leagent admin password (default: from .env LEAGENT_ADMIN_PASSWORD)",
    )
    ed.add_argument(
        "--dry-run",
        action="store_true",
        help="Show what would be edited without doing it",
    )

    # Delete — remove a model from Leagent
    dl = sub.add_parser("delete", help="Delete a model from Leagent")
    dl.add_argument("model", help="Model name or ID to delete")
    dl.add_argument(
        "--leagent-url",
        default="http://10.110.158.146:8000",
        help="Leagent backend URL (default: http://10.110.158.146:8000)",
    )
    dl.add_argument(
        "--supabase-url",
        default=None,
        help="Supabase URL for Leagent auth (default: from .env SUPABASE_URL)",
    )
    dl.add_argument(
        "--anon-key",
        default=None,
        help="Supabase anon key (default: from .env SUPABASE_ANON_KEY)",
    )
    dl.add_argument(
        "--admin-email",
        default=None,
        help="Leagent admin email (default: from .env LEAGENT_ADMIN_EMAIL)",
    )
    dl.add_argument(
        "--admin-password",
        default=None,
        help="Leagent admin password (default: from .env LEAGENT_ADMIN_PASSWORD)",
    )
    dl.add_argument(
        "--dry-run",
        action="store_true",
        help="Show what would be deleted without doing it",
    )

    args = p.parse_args()

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
    elif args.command == "ws":
        if args.ws_cmd == "create":
            cmd_ws_create(args)
        elif args.ws_cmd == "list":
            cmd_ws_list(args)
        elif args.ws_cmd == "status":
            cmd_ws_status(args)
        elif args.ws_cmd in ("restart", "stop", "save", "delete"):
            cmd_ws_action(args)
    elif args.command == "endpoint":
        cmd_endpoint(args)
    elif args.command == "register":
        cmd_register(args)
    elif args.command == "edit":
        cmd_edit(args)
    elif args.command == "delete":
        cmd_delete(args)


if __name__ == "__main__":
    main()
