import { normalize } from "@larksuiteoapi/node-sdk";

// Feishu's maintained converter owns post/forwarded-message formatting. Reads
// happen after durable ingress, so a slow history API cannot delay the WS ACK.
export function feishuContextReader(
  client,
  botIdentity,
  historyEnabled = false,
) {
  const historyCache = new Map();
  const get = async (id) => {
    const result = await client.im.message.get({ path: { message_id: id } });
    if (result.code !== 0) throw new Error("Context unavailable");
    return result.data?.items || [];
  };
  const convert = async (item, row, children) => {
    const value = await normalize(
      {
        sender: {
          sender_id: { open_id: item.sender?.id || row.sender_id },
          sender_type: "user",
        },
        message: {
          message_id: item.message_id,
          chat_id: row.chat_id,
          chat_type: row.chat_type,
          message_type: item.msg_type,
          content: item.body?.content || "{}",
          mentions: item.mentions || [],
        },
      },
      {
        botIdentity,
        stripBotMentions: true,
        fetchSubMessages: () => Promise.resolve(children || []),
      },
    );
    return {
      text: value.content,
      resources: value.resources.map((resource) => ({
        ...resource,
        message_id: item.message_id,
      })),
    };
  };
  return async (row, initial) => {
    const resources = [...(row.resources || [])];
    const excerpts = [],
      notices = [];
    let text = row.text;
    if (row.raw_content_type === "merge_forward") {
      try {
        const children = (await get(row.message_id)).slice(0, 50);
        const parent = children.find(
          (item) => item.message_id === row.message_id,
        );
        if (parent) text = (await convert(parent, row, children)).text;
        for (const child of children.filter(
          (item) =>
            item.message_id !== row.message_id &&
            item.msg_type !== "merge_forward",
        ))
          resources.push(...(await convert(child, row)).resources);
      } catch {
        notices.push("无法读取合并转发的内容，请直接发送需要处理的消息。");
      }
    }
    let quote = row.reply_to_message_id;
    const seen = new Set([row.message_id]);
    for (let depth = 0; quote && depth < 4 && !seen.has(quote); depth++) {
      seen.add(quote);
      try {
        const items = await get(quote),
          item = items.find((item) => item.message_id === quote);
        if (!item || (item.chat_id && item.chat_id !== row.chat_id)) break;
        const value = await convert(item, row, items);
        excerpts.unshift(
          `Quoted message (${item.sender?.id || "unknown"}):\n${value.text}`,
        );
        resources.push(...value.resources);
        quote = item.parent_id;
      } catch {
        notices.push("引用消息暂不可读取，可直接发送相关内容。");
        break;
      }
    }
    if (initial && historyEnabled && row.chat_type === "group") {
      let cached = historyCache.get(row.chat_id);
      if (!cached || cached.expires < Date.now()) {
        try {
          const result = await client.im.message.list({
            params: {
              container_id_type: "chat",
              container_id: row.chat_id,
              page_size: 20,
              sort_type: "ByCreateTimeDesc",
              end_time: String(
                Math.floor((row.occurred_at || row.created_at) / 1000),
              ),
            },
          });
          if (result.code !== 0) throw new Error("History unavailable");
          cached = {
            expires: Date.now() + 30000,
            items: result.data?.items || [],
          };
        } catch {
          cached = {
            expires: Date.now() + 300000,
            items: [],
            notice: "群历史暂不可读取，请检查机器人的历史消息权限。",
          };
        }
        historyCache.set(row.chat_id, cached);
        if (historyCache.size > 100)
          historyCache.delete(historyCache.keys().next().value);
      }
      if (cached.notice) notices.push(cached.notice);
      const lines = [];
      for (const item of [...cached.items].reverse()) {
        if (
          seen.has(item.message_id) ||
          Number(item.create_time) > (row.occurred_at || row.created_at) ||
          item.msg_type === "merge_forward"
        )
          continue;
        const value = await convert(item, row);
        lines.push(`${item.sender?.id || "unknown"}: ${value.text}`);
      }
      if (lines.length)
        excerpts.unshift(
          `Recent group messages (context):\n${lines.join("\n").slice(-12000)}`,
        );
    }
    const unique = [
      ...new Map(
        resources.map((resource) => [
          `${resource.message_id || row.message_id}:${resource.fileKey}`,
          resource,
        ]),
      ).values(),
    ];
    return {
      text: [...excerpts, text].join("\n\n").slice(-40000),
      resources: unique,
      notice: notices.join(" "),
    };
  };
}
