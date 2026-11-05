// packetchat.test.ts — the selections of the quick chat. A wrong selection
// shows the conversation of another packet, or sends the operator to a
// conversation that holds no message.

import { describe, expect, test } from "bun:test";

import { chatTitle, currentConversation, earlierConversation, queueHidden } from "./packetchat.js";

const packet = (packet_id: number, current: boolean, task_id = 7) =>
  ({ packet_id, version_id: packet_id * 10, task_id, task_title: "Raise the limit", project_id: "alpha", current });

const sessions = [
  { id: "h-ordinary", first_message: "Hello", vision_project: null, packet: null },
  { id: "h-vision", first_message: "Plan", vision_project: "alpha", packet: null },
  { id: "h-10", first_message: "About packet 10", vision_project: null, packet: packet(10, true) },
  { id: "h-12-ended", first_message: "An older question", vision_project: null, packet: packet(12, false) },
  { id: "h-12", first_message: "About packet 12", vision_project: null, packet: packet(12, true) },
  { id: "h-14-other-task", first_message: "Another task", vision_project: null, packet: packet(14, true, 8) },
  { id: "h-15", first_message: null, vision_project: null, packet: packet(15, true) },
];

describe("currentConversation", () => {
  test("currentConversation selects the current conversation of the open packet", () => {
    expect(currentConversation(sessions, 12)?.id).toBe("h-12");
    expect(currentConversation(sessions, 15)?.id).toBe("h-15");
  });

  test("currentConversation gives null for a packet without a current conversation", () => {
    expect(currentConversation(sessions, 11)).toBeNull();
    expect(currentConversation([{ id: "h-old", first_message: "Hi", packet: packet(11, false) }], 11)).toBeNull();
    expect(currentConversation(undefined, 12)).toBeNull();
  });
});

describe("earlierConversation", () => {
  test("earlierConversation selects the current conversation of the packet that the packet row names", () => {
    // The row of packet 15 names packet 12, and the row of packet 12 names packet 10.
    expect(earlierConversation(sessions, 12)?.id).toBe("h-12");
    expect(earlierConversation(sessions, 10)?.id).toBe("h-10");
  });

  test("earlierConversation gives null when the earlier packet has no conversation with a message", () => {
    const empty = sessions.map((row) => (row.id === "h-12" ? { ...row, first_message: null } : row));
    // The conversation of packet 10 holds a message, but packet 12 is the earlier packet.
    expect(earlierConversation(empty, 12)).toBeNull();
    const ended = sessions.filter((row) => row.id !== "h-12");
    expect(earlierConversation(ended, 12)).toBeNull();
    // The first packet of a task names no earlier packet.
    expect(earlierConversation(sessions, null)).toBeNull();
    expect(earlierConversation(sessions, undefined)).toBeNull();
  });

  test("earlierConversation gives null when the earlier packet has no list entry and an older packet has a conversation", () => {
    // The Revise decision returned packet 12, and packet 12 has no
    // conversation. Packet 10 is older than the earlier packet.
    const withoutTwelve = [{ id: "h-10", first_message: "About packet 10", vision_project: null, packet: packet(10, true) }];
    expect(earlierConversation(withoutTwelve, 12)).toBeNull();
  });
});

describe("queueHidden", () => {
  test("queueHidden hides the queue column when the reader beside it would be narrower than 640 px", () => {
    // The content widths of the Review panel at a window width of 1280, 1440,
    // and 1920 px.
    expect(queueHidden(1208)).toBe(true);
    expect(queueHidden(1368)).toBe(false);
    expect(queueHidden(1848)).toBe(false);
    // 1264 px gives a reader of exactly 640 px beside the 200 px queue column.
    expect(queueHidden(1264)).toBe(false);
    expect(queueHidden(1263)).toBe(true);
  });
});

describe("chatTitle", () => {
  test("chatTitle uses the format of the reader title", () => {
    expect(chatTitle(4, "Raise the limit")).toBe("Task #4 · Raise the limit");
    expect(chatTitle(4, "")).toBe("Task #4");
  });
});
