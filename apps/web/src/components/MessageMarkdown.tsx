import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

/** Agent output as Markdown, including GitHub tables, task lists and strikethrough. */
export function MessageMarkdown({ content }: { content: string }) {
  return (
    <div className="message-markdown">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={{ h1: ({ children }) => <h2>{children}</h2> }}>
        {content}
      </ReactMarkdown>
    </div>
  );
}
