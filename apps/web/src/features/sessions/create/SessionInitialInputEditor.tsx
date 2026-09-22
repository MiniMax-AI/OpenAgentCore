import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import { useId, useRef } from "react";

import {
  projectSessionInitialInput,
  sessionInitialInputDraftReducer,
  type SessionInitialInputDraft,
  type SessionInitialInputDraftAction,
} from "./session-initial-input";

import "./SessionInitialInputEditor.css";

export interface SessionInitialInputEditorProps {
  draft: SessionInitialInputDraft;
  disabled?: boolean;
  required?: boolean;
  showTextField?: boolean;
  onChange: (draft: SessionInitialInputDraft) => void;
}

export function SessionInitialInputEditor({
  draft,
  disabled = false,
  required = false,
  showTextField = true,
  onChange,
}: SessionInitialInputEditorProps) {
  const id = useId();
  const nextIdRef = useRef(0);
  const projection = projectSessionInitialInput(draft);

  const nextId = (kind: "message" | "part") => {
    nextIdRef.current += 1;
    return `${id}-${kind}-${nextIdRef.current}`;
  };
  const dispatch = (action: SessionInitialInputDraftAction) => {
    onChange(sessionInitialInputDraftReducer(draft, action));
  };
  const selectMessages = () => {
    dispatch({
      type: "set-mode",
      mode: "messages",
      seed: { messageId: nextId("message"), partId: nextId("part") },
    });
  };
  const addMessage = () => {
    dispatch({
      type: "add-message",
      message: {
        id: nextId("message"),
        parts: [{ id: nextId("part"), text: "" }],
      },
    });
  };

  return (
    <section className="session-initial-input-editor" aria-labelledby={`${id}-heading`}>
      <div className="session-initial-input-heading">
        <div>
          <span id={`${id}-heading`}>Initial input</span>
          <small>User text only. Assistant messages and images are not supported here.</small>
        </div>
      </div>

      <fieldset className="session-initial-input-modes" disabled={disabled}>
        <legend>Input format</legend>
        <label>
          <input
            type="radio"
            name={`${id}-mode`}
            value="text"
            checked={draft.mode === "text"}
            onChange={() => dispatch({ type: "set-mode", mode: "text" })}
          />
          Text
        </label>
        <label>
          <input
            type="radio"
            name={`${id}-mode`}
            value="messages"
            checked={draft.mode === "messages"}
            onChange={selectMessages}
          />
          Message array
        </label>
      </fieldset>

      <p className="session-initial-input-switch-note" role="note">
        Text and message-array drafts stay separate when you switch. Only the selected format is submitted.
      </p>

      {draft.mode === "text" && showTextField ? (
        <label className="field session-initial-text-field">
          <span>First user message</span>
          <textarea
            aria-label="Initial input"
            value={draft.text}
            onChange={(event) => dispatch({ type: "set-text", value: event.target.value })}
            rows={5}
            placeholder={required ? "Write the first message…" : "Optional first message…"}
            aria-required={required}
            disabled={disabled}
          />
          <small>{required ? "Required without an Environment. " : "Optional. "}Nonblank input is preserved exactly and starts the initial Turn during Session creation.</small>
        </label>
      ) : draft.mode === "text" ? (
        <p className="session-initial-input-switch-note">
          The basic First message field is active. Select Message array here only when ordered user messages or multiple text parts are required.
        </p>
      ) : (
        <div className="session-initial-message-builder">
          <div className="session-initial-message-builder-heading">
            <div>
              <strong>Ordered user messages</strong>
              <span>Each message contains one or more ordered text parts.</span>
            </div>
            <button
              className="button outline"
              type="button"
              onClick={addMessage}
              disabled={disabled}
            >
              <Plus size={13} aria-hidden="true" />
              Add message
            </button>
          </div>

          <ol className="session-initial-messages">
            {draft.messages.map((message, messageIndex) => (
              <li className="session-initial-message" key={message.id}>
                <div className="session-initial-message-heading">
                  <strong>User message {messageIndex + 1}</strong>
                  <div className="session-initial-editor-actions">
                    <button
                      className="icon-button"
                      type="button"
                      aria-label={`Move user message ${messageIndex + 1} up`}
                      title="Move message up"
                      disabled={disabled || messageIndex === 0}
                      onClick={() => dispatch({
                        type: "move-message",
                        messageId: message.id,
                        direction: -1,
                      })}
                    >
                      <ArrowUp size={14} aria-hidden="true" />
                    </button>
                    <button
                      className="icon-button"
                      type="button"
                      aria-label={`Move user message ${messageIndex + 1} down`}
                      title="Move message down"
                      disabled={disabled || messageIndex === draft.messages.length - 1}
                      onClick={() => dispatch({
                        type: "move-message",
                        messageId: message.id,
                        direction: 1,
                      })}
                    >
                      <ArrowDown size={14} aria-hidden="true" />
                    </button>
                    <button
                      className="icon-button danger"
                      type="button"
                      aria-label={`Remove user message ${messageIndex + 1}`}
                      title="Remove message"
                      disabled={disabled || draft.messages.length === 1}
                      onClick={() => dispatch({
                        type: "remove-message",
                        messageId: message.id,
                      })}
                    >
                      <Trash2 size={14} aria-hidden="true" />
                    </button>
                  </div>
                </div>

                <ol className="session-initial-parts">
                  {message.parts.map((part, partIndex) => (
                    <li className="session-initial-part" key={part.id}>
                      <div className="session-initial-part-heading">
                        <label htmlFor={`${id}-${part.id}`}>Text part {partIndex + 1}</label>
                        <div className="session-initial-editor-actions">
                          <button
                            className="icon-button"
                            type="button"
                            aria-label={`Move text part ${partIndex + 1} of user message ${messageIndex + 1} up`}
                            title="Move text part up"
                            disabled={disabled || partIndex === 0}
                            onClick={() => dispatch({
                              type: "move-part",
                              messageId: message.id,
                              partId: part.id,
                              direction: -1,
                            })}
                          >
                            <ArrowUp size={14} aria-hidden="true" />
                          </button>
                          <button
                            className="icon-button"
                            type="button"
                            aria-label={`Move text part ${partIndex + 1} of user message ${messageIndex + 1} down`}
                            title="Move text part down"
                            disabled={disabled || partIndex === message.parts.length - 1}
                            onClick={() => dispatch({
                              type: "move-part",
                              messageId: message.id,
                              partId: part.id,
                              direction: 1,
                            })}
                          >
                            <ArrowDown size={14} aria-hidden="true" />
                          </button>
                          <button
                            className="icon-button danger"
                            type="button"
                            aria-label={`Remove text part ${partIndex + 1} from user message ${messageIndex + 1}`}
                            title="Remove text part"
                            disabled={disabled || message.parts.length === 1}
                            onClick={() => dispatch({
                              type: "remove-part",
                              messageId: message.id,
                              partId: part.id,
                            })}
                          >
                            <Trash2 size={14} aria-hidden="true" />
                          </button>
                        </div>
                      </div>
                      <textarea
                        id={`${id}-${part.id}`}
                        value={part.text}
                        onChange={(event) => dispatch({
                          type: "update-part",
                          messageId: message.id,
                          partId: part.id,
                          value: event.target.value,
                        })}
                        rows={3}
                        placeholder="Required user text…"
                        aria-invalid={!message.parts.some((candidate) => candidate.text.match(/[^\p{White_Space}]/u))}
                        disabled={disabled}
                      />
                    </li>
                  ))}
                </ol>

                <button
                  className="button outline session-initial-add-part"
                  type="button"
                  disabled={disabled}
                  onClick={() => dispatch({
                    type: "add-part",
                    messageId: message.id,
                    part: { id: nextId("part"), text: "" },
                  })}
                >
                  <Plus size={13} aria-hidden="true" />
                  Add text part
                </button>
              </li>
            ))}
          </ol>

          {"error" in projection ? (
            <small className="field-error" role="alert">{projection.error}</small>
          ) : null}
        </div>
      )}
    </section>
  );
}
