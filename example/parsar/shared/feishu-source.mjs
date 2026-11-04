// The line that tells an Agent which Feishu message it is answering. Parsar's
// own views show the message without it.
export const feishuSource = (row) =>
  `[飞书消息 message_id=${row.message_id} chat_id=${row.chat_id} chat_type=${row.chat_type} sender_open_id=${row.sender_id}]`;

export const withoutFeishuSource = (text) => text.replace(/^\[飞书消息 message_id=\S+ chat_id=\S+ chat_type=\S+ sender_open_id=\S+\]\n/, "");
