export type MessagePart =
  | {
      type: "text";
      text: string;
    }
  | {
      type: "sticker";
      pack_id?: string;
      sticker_id: string;
      alt?: string;
    };
