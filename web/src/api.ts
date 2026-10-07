export async function api<T>(
  route: string,
  params: Record<string, string> = {},
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(`/api/${route}?${new URLSearchParams(params)}`, {
    signal,
  });
  const data = await response.json();
  if (!response.ok)
    throw new Error(data.error || "The request could not be completed.");
  return data;
}
