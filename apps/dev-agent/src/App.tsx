export default function App() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <header className="flex flex-col gap-2">
          <p className="text-sm text-muted-foreground">Dev Agent</p>
          <h1 className="text-2xl font-semibold">Desktop Runtime Shell</h1>
        </header>
        <section className="rounded-lg border border-border bg-card p-6 text-sm text-muted-foreground">
          This is the Tauri desktop UI scaffold. Wire IPC or local HTTP calls to
          the agent runtime here.
        </section>
      </div>
    </div>
  );
}
