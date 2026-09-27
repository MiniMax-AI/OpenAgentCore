import { describe, expect, it } from "vitest";

import { enqueueToast, type ToastItem } from "./Toast";

describe("console toast queue", () => {
  it("refreshes an identical keyed toast and advances its timer revision", () => {
    const first = enqueueToast([], 1, "Agent created.", {
      tone: "success",
      key: "success:Agent created.",
    });
    const refreshed = enqueueToast(first, 2, "Agent created.", {
      tone: "success",
      key: "success:Agent created.",
    });

    expect(refreshed).toHaveLength(1);
    expect(refreshed[0]).toMatchObject({
      id: 1,
      revision: 2,
      leaving: false,
      message: "Agent created.",
    });
  });

  it("keeps at most three transient toasts live while the oldest exits", () => {
    let queue: ToastItem[] = [];
    for (let id = 1; id <= 4; id += 1) {
      queue = enqueueToast(queue, id, `Notice ${id}`);
    }

    expect(queue.filter((toast) => !toast.leaving)).toHaveLength(3);
    expect(queue.find((toast) => toast.id === 1)?.leaving).toBe(true);
  });

  it("revives a keyed toast that was already leaving", () => {
    const leaving = enqueueToast([], 1, "Connection failed.", {
      tone: "error",
      key: "error:Connection failed.",
    }).map((toast) => ({ ...toast, leaving: true }));

    const revived = enqueueToast(leaving, 2, "Connection failed.", {
      tone: "error",
      key: "error:Connection failed.",
    });

    expect(revived).toHaveLength(1);
    expect(revived[0]).toMatchObject({ id: 1, revision: 2, leaving: false });
  });
});
