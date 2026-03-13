export default function App() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <header className="flex flex-col gap-2">
          <p className="text-sm text-muted-foreground">Bridge Admin</p>
          <h1 className="text-2xl font-semibold">Runtime Console Skeleton</h1>
        </header>
        <section className="rounded-lg border border-border bg-card p-6 text-sm text-muted-foreground">
          This is the embedded Bridge admin UI scaffold. Hook routing, session, and
          tunnel views here as the runtime layers come online.
        </section>
      </div>
    </div>
  );
}
