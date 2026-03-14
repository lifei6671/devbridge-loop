export type RuntimeEventEnvelope<TPayload> = { payload: TPayload };

export type ListenFn<TPayload> = (
  eventName: string,
  handler: (event: RuntimeEventEnvelope<TPayload>) => void,
) => Promise<() => void>;

export declare function registerManagedListener<TPayload>(
  listenFn: ListenFn<TPayload>,
  eventName: string,
  onPayload: (payload: TPayload) => void,
  isDisposed: () => boolean,
): Promise<(() => void) | null>;
