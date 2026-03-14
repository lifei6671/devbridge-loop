/**
 * 注册事件监听并在组件已卸载时自动回收延迟注册的监听器。
 *
 * @template TPayload
 * @param {(eventName: string, handler: (event: { payload: TPayload }) => void) => Promise<() => void>} listenFn
 * @param {string} eventName
 * @param {(payload: TPayload) => void} onPayload
 * @param {() => boolean} isDisposed
 * @returns {Promise<(() => void) | null>}
 */
export async function registerManagedListener(listenFn, eventName, onPayload, isDisposed) {
  if (isDisposed()) {
    return null;
  }
  const nextUnlisten = await listenFn(eventName, (event) => {
    onPayload(event.payload);
  });
  if (isDisposed()) {
    nextUnlisten();
    return null;
  }
  return nextUnlisten;
}
