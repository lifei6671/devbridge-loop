import test from "node:test";
import assert from "node:assert/strict";

import { registerManagedListener } from "../src/runtime_subscription.js";

test("registerManagedListener should dispose late subscription when already unmounted", async () => {
  let disposed = false;
  let handler = null;
  let unlistenCalls = 0;
  let resolveListen;
  const listenFn = (_eventName, nextHandler) => {
    handler = nextHandler;
    return new Promise((resolve) => {
      resolveListen = resolve;
    });
  };

  const registerPromise = registerManagedListener(
    listenFn,
    "agent-runtime-changed",
    () => {},
    () => disposed,
  );

  disposed = true;
  resolveListen(() => {
    unlistenCalls += 1;
  });
  const unlisten = await registerPromise;

  assert.equal(unlisten, null);
  assert.equal(unlistenCalls, 1);
  assert.equal(typeof handler, "function");
});

test("registerManagedListener should forward payload and keep unlisten when mounted", async () => {
  const seenPayloads = [];
  let unlistenCalls = 0;
  const listenFn = async (_eventName, nextHandler) => {
    nextHandler({ payload: { snapshot: { pid: 42 } } });
    return () => {
      unlistenCalls += 1;
    };
  };

  const unlisten = await registerManagedListener(
    listenFn,
    "agent-runtime-changed",
    (payload) => {
      seenPayloads.push(payload);
    },
    () => false,
  );

  assert.equal(typeof unlisten, "function");
  assert.equal(seenPayloads.length, 1);
  assert.deepEqual(seenPayloads[0], { snapshot: { pid: 42 } });
  unlisten();
  assert.equal(unlistenCalls, 1);
});
