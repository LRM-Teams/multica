"use client";

import { ChevronRight, File, FileCode, FileImage, FileJson, FileText, Folder } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";

export interface FileTreeInputNode {
  path: string;
  is_dir: boolean;
  size?: number;
}

export interface FileTreeNode {
  name: string;
  path: string;
  isDir: boolean;
  size?: number;
  children: FileTreeNode[];
}

// Rebuild a nested tree from a flat, slash-separated node list. Intermediate
// directories are synthesized if the backend didn't list them explicitly.
export function buildFileTree(nodes: readonly FileTreeInputNode[]): FileTreeNode[] {
  const root: FileTreeNode = { name: "", path: "", isDir: true, children: [] };
  const byPath = new Map<string, FileTreeNode>([["", root]]);

  for (const n of nodes) {
    const parts = n.path.split("/").filter(Boolean);
    let parent = root;
    let acc = "";
    for (let i = 0; i < parts.length; i++) {
      const part = parts[i]!;
      acc = acc ? `${acc}/${part}` : part;
      let node = byPath.get(acc);
      if (!node) {
        const isLeaf = i === parts.length - 1;
        node = {
          name: part,
          path: acc,
          isDir: isLeaf ? n.is_dir : true,
          size: isLeaf ? n.size : undefined,
          children: [],
        };
        byPath.set(acc, node);
        parent.children.push(node);
      } else if (i === parts.length - 1) {
        node.isDir = n.is_dir;
        node.size = n.size;
      }
      parent = node;
    }
  }

  const sortRec = (t: FileTreeNode) => {
    t.children.sort((a, b) =>
      a.isDir !== b.isDir ? (a.isDir ? -1 : 1) : a.name.localeCompare(b.name),
    );
    t.children.forEach(sortRec);
  };
  sortRec(root);
  return root.children;
}

export function fileLanguage(name: string): string {
  const ext = name.includes(".") ? name.slice(name.lastIndexOf(".") + 1).toLowerCase() : "";
  return ext || "text";
}

// Per-extension icon + color so file kinds are scannable, VSCode-style.
export function fileMeta(name: string): { Icon: typeof File; className: string } {
  const ext = fileLanguage(name);
  switch (ext) {
    case "ts":
    case "tsx":
    case "js":
    case "jsx":
    case "mjs":
    case "cjs":
      return { Icon: FileCode, className: "text-sky-500" };
    case "go":
      return { Icon: FileCode, className: "text-cyan-500" };
    case "py":
      return { Icon: FileCode, className: "text-amber-500" };
    case "rs":
    case "java":
    case "c":
    case "cpp":
    case "rb":
    case "php":
    case "sh":
      return { Icon: FileCode, className: "text-orange-500" };
    case "html":
    case "vue":
    case "svelte":
      return { Icon: FileCode, className: "text-orange-600" };
    case "css":
    case "scss":
    case "less":
      return { Icon: FileCode, className: "text-blue-500" };
    case "json":
    case "yaml":
    case "yml":
    case "toml":
      return { Icon: FileJson, className: "text-amber-600" };
    case "md":
    case "mdx":
    case "txt":
      return { Icon: FileText, className: "text-muted-foreground" };
    case "png":
    case "jpg":
    case "jpeg":
    case "gif":
    case "svg":
    case "webp":
    case "ico":
      return { Icon: FileImage, className: "text-purple-500" };
    default:
      return { Icon: File, className: "text-muted-foreground" };
  }
}

const INDENT = 12;

function FileTreeRow({
  node,
  depth,
  isCollapsed,
  toggle,
  onOpenFile,
}: {
  node: FileTreeNode;
  depth: number;
  isCollapsed: (path: string) => boolean;
  toggle: (path: string) => void;
  onOpenFile: (path: string) => void;
}) {
  if (node.isDir) {
    const collapsed = isCollapsed(node.path);
    return (
      <>
        <button
          type="button"
          onClick={() => toggle(node.path)}
          style={{ paddingLeft: depth * INDENT + 6 }}
          className="flex w-full items-center gap-1 rounded py-1 pr-2 text-left text-xs hover:bg-accent"
        >
          <ChevronRight
            className={cn(
              "size-3.5 shrink-0 text-muted-foreground transition-transform",
              !collapsed && "rotate-90",
            )}
          />
          <Folder className="size-4 shrink-0 text-sky-500" />
          <span className="truncate text-foreground">{node.name}</span>
        </button>
        {!collapsed &&
          node.children.map((c) => (
            <FileTreeRow
              key={c.path}
              node={c}
              depth={depth + 1}
              isCollapsed={isCollapsed}
              toggle={toggle}
              onOpenFile={onOpenFile}
            />
          ))}
      </>
    );
  }
  const { Icon, className } = fileMeta(node.name);
  return (
    <button
      type="button"
      onClick={() => onOpenFile(node.path)}
      style={{ paddingLeft: depth * INDENT + 6 + 18 }}
      className="flex w-full items-center gap-1.5 rounded py-1 pr-2 text-left text-xs hover:bg-accent"
    >
      <Icon className={cn("size-4 shrink-0", className)} />
      <span className="truncate text-foreground">{node.name}</span>
    </button>
  );
}

export function FileTree({
  tree,
  collapsed,
  onToggle,
  onOpenFile,
}: {
  tree: readonly FileTreeNode[];
  collapsed: ReadonlySet<string>;
  onToggle: (path: string) => void;
  onOpenFile: (path: string) => void;
}) {
  return (
    <>
      {tree.map((node) => (
        <FileTreeRow
          key={node.path}
          node={node}
          depth={0}
          isCollapsed={(p) => collapsed.has(p)}
          toggle={onToggle}
          onOpenFile={onOpenFile}
        />
      ))}
    </>
  );
}
