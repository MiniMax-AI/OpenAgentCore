// The line that tells an Agent which Feishu message it is answering and how its
// reply gets back there. Parsar's own views show the message without it.
export const feishuSource = (row) =>
  `[飞书消息 message_id=${row.message_id} chat_id=${row.chat_id} chat_type=${row.chat_type} sender_open_id=${row.sender_id}；回复写在最终回复里，Parsar 会把它发回这条消息所在的对话；要给对方看的图片或文件放进工作区的 outputs 目录，并在回复里写出文件名；不要用 lark-cli 往这个对话发消息]`;

export const withoutFeishuSource = (text) =>
  text.replace(/^\[飞书消息 message_id=\S+ chat_id=\S+ chat_type=\S+ sender_open_id=[^\s；\]]+(；[^\]\n]*)?\]\n/, "");
