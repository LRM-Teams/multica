import { Extension } from "@tiptap/core";
import type { Node as ProseMirrorNode, NodeType } from "@tiptap/pm/model";
import { TextSelection, type Transaction } from "@tiptap/pm/state";

function childrenOf(node: ProseMirrorNode) {
  const children: ProseMirrorNode[] = [];
  node.forEach((child) => children.push(child));
  return children;
}

function makeListItemFromParagraph(
  listItemType: NodeType,
  paragraph: ProseMirrorNode,
) {
  return listItemType.create(null, paragraph.copy(paragraph.content));
}

function setSelectionToLastParagraph(tr: Transaction, from: number, to: number) {
  let target: number | null = null;
  tr.doc.nodesBetween(from, Math.min(to, tr.doc.content.size), (node, pos) => {
    if (node.type.name === "paragraph") target = pos + 1;
    return true;
  });
  if (target !== null) tr.setSelection(TextSelection.create(tr.doc, target));
}

/**
 * PowerPoint/Notion-style outline indent for plain note lines.
 *
 * ProseMirror cannot nest arbitrary paragraphs under paragraphs, so plain-line
 * indentation is represented as a bullet list where the current paragraph is a
 * child list item of the previous paragraph/list item.
 */
export const BlockIndentExtension = Extension.create({
  name: "blockIndent",

  addKeyboardShortcuts() {
    return {
      Tab: () => {
        const { state, view } = this.editor;
        const { selection } = state;
        if (!selection.empty) return false;
        const $from = selection.$from;
        if ($from.depth !== 1 || $from.parentOffset !== 0) return false;

        const currentIndex = $from.index(0);
        if (currentIndex === 0) return false;

        const bulletListType = state.schema.nodes.bulletList;
        const listItemType = state.schema.nodes.listItem;
        const paragraphType = state.schema.nodes.paragraph;
        if (!bulletListType || !listItemType || !paragraphType) return false;

        const current = state.doc.child(currentIndex);
        const previous = state.doc.child(currentIndex - 1);
        if (current.type !== paragraphType) return false;

        const currentPos = $from.before(1);
        const previousPos = currentPos - previous.nodeSize;
        const replaceEnd = currentPos + current.nodeSize;
        let replacement: ProseMirrorNode | null = null;

        if (previous.type === paragraphType) {
          replacement = bulletListType.create(null, [
            listItemType.create(null, [
              previous.copy(previous.content),
              bulletListType.create(null, [makeListItemFromParagraph(listItemType, current)]),
            ]),
          ]);
        } else if (previous.type === bulletListType) {
          const topItems = childrenOf(previous);
          const lastItem = topItems.at(-1);
          if (!lastItem || lastItem.type !== listItemType) return false;

          const lastItemChildren = childrenOf(lastItem);
          const lastChild = lastItemChildren.at(-1);
          const childItem = makeListItemFromParagraph(listItemType, current);
          const nestedList =
            lastChild?.type === bulletListType
              ? bulletListType.create(lastChild.attrs, [...childrenOf(lastChild), childItem], lastChild.marks)
              : bulletListType.create(null, [childItem]);
          const nextLastItem = listItemType.create(
            lastItem.attrs,
            lastChild?.type === bulletListType
              ? [...lastItemChildren.slice(0, -1), nestedList]
              : [...lastItemChildren, nestedList],
            lastItem.marks,
          );
          replacement = bulletListType.create(previous.attrs, [...topItems.slice(0, -1), nextLastItem], previous.marks);
        }

        if (!replacement) return false;
        const tr = state.tr.replaceWith(previousPos, replaceEnd, replacement);
        tr.setMeta("skipTrailingNode", true);
        setSelectionToLastParagraph(tr, previousPos, previousPos + replacement.nodeSize);
        view.dispatch(tr.scrollIntoView());
        return true;
      },
    };
  },
});
