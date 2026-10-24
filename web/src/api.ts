async function parse<T>(response: Response): Promise<T> {
  const text = await response.text();
  let data;
  try {
    data = JSON.parse(text);
  } catch {
    // Plain-text errors come from the server's request checks.
    throw new Error(text.trim() || "The request could not be completed.");
  }
  if (!response.ok)
    throw new Error(data.error || "The request could not be completed.");
  return data;
}

export async function api<T>(
  route: string,
  params: Record<string, string> = {},
  signal?: AbortSignal,
): Promise<T> {
  return parse<T>(
    await fetch(`/api/${route}?${new URLSearchParams(params)}`, { signal }),
  );
}

export async function send<T>(
  method: "POST" | "PATCH" | "DELETE",
  route: string,
  body?: unknown,
  params: Record<string, string> = {},
): Promise<T> {
  return parse<T>(
    await fetch(`/api/${route}?${new URLSearchParams(params)}`, {
      method,
      ...(body === undefined
        ? {}
        : {
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          }),
    }),
  );
}
