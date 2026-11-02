import { useEffect, useState } from "react";

type Readiness = "checking" | "ready" | "error";

export default function App() {
  const [readiness, setReadiness] = useState<Readiness>("checking");

  useEffect(() => {
    const controller = new AbortController();

    async function checkReadiness() {
      try {
        const response = await fetch("/health/ready", {
          signal: controller.signal,
        });
        if (!response.ok) {
          throw new Error("API readiness check failed");
        }
        setReadiness("ready");
      } catch (error) {
        if (error instanceof DOMException && error.name === "AbortError") {
          return;
        }
        setReadiness("error");
      }
    }

    void checkReadiness();
    return () => controller.abort();
  }, []);

  return (
    <main className="shell">
      <header className="masthead">
        <p className="eyebrow">Release-risk diagnostics</p>
        <h1>Relythra</h1>
        <p>Review service health before investigating a release.</p>
      </header>

      <section className="status-card" aria-labelledby="service-status">
        <h2 id="service-status">Service status</h2>
        {readiness === "checking" && (
          <p role="status">Checking API readiness…</p>
        )}
        {readiness === "ready" && <p role="status">API is ready</p>}
        {readiness === "error" && (
          <p role="alert">
            API is not ready. Check the API and database, then retry.
          </p>
        )}
      </section>
    </main>
  );
}
