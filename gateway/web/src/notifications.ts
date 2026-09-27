import { toast, type ExternalToast } from "sonner";

export function errorMessage(error: unknown, fallback = "请求失败") {
  if (error instanceof Error && error.message) return error.message;
  if (typeof error === "string" && error) return error;
  return fallback;
}

export function notifyError(error: unknown, options?: ExternalToast) {
  if (
    error &&
    typeof error === "object" &&
    "status" in error &&
    (error as { status?: unknown }).status === 401
  )
    return;
  return toast.error(errorMessage(error), options);
}

export function notifyRetry(
  error: unknown,
  retry: () => void | Promise<void>,
  options?: ExternalToast,
) {
  return notifyError(error, {
    ...options,
    action: {
      label: "重试",
      onClick: () => void retry(),
    },
  });
}
