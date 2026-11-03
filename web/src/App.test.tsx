import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import App from "./App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("App", () => {
  it("shows API readiness when the health check succeeds", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ status: "ready" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    render(<App />);

    expect(screen.getByRole("heading", { name: "Relythra" })).toBeInTheDocument();
    expect(await screen.findByText("API is ready")).toBeInTheDocument();
  });

  it("shows a useful error when the API is unreachable", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));

    render(<App />);

    expect(screen.getByRole("heading", { name: "Relythra" })).toBeInTheDocument();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "API is not ready. Check the API and database, then retry.",
    );
  });

  it("shows a useful error when readiness returns 503", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ status: "not_ready" }), { status: 503 }),
      ),
    );

    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "API is not ready. Check the API and database, then retry.",
    );
  });
});
