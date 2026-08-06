"use client";

import { ChevronRight, Folder } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { fileMeta, type FileTreeNode } from "./file-tree-utils";

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
          aria-expanded={!collapsed}
          style={{ paddingLeft: depth * INDENT + 6 }}
          className="flex min-w-0 w-full items-center gap-1 rounded py-1 pr-2 text-left text-xs hover:bg-accent"
        >
          <ChevronRight
            className={cn(
              "size-3.5 shrink-0 text-muted-foreground transition-transform",
              !collapsed && "rotate-90",
            )}
          />
          <Folder className="size-4 shrink-0 text-sky-500" />
          <span className="min-w-0 flex-1 truncate text-foreground">{node.name}</span>
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
      className="flex min-w-0 w-full items-center gap-1.5 rounded py-1 pr-2 text-left text-xs hover:bg-accent"
    >
      <Icon className={cn("size-4 shrink-0", className)} />
      <span className="min-w-0 flex-1 truncate text-foreground">{node.name}</span>
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
