import { Toaster as Sonner, type ToasterProps } from "sonner";

const Toaster = (props: ToasterProps): JSX.Element => (
  <Sonner
    theme="light"
    richColors
    closeButton
    position="top-right"
    toastOptions={{
      classNames: {
        toast: "rounded-xl border border-[#d8deea] bg-white text-[#1f2b40] shadow-[0_10px_32px_rgba(25,42,77,0.14)]",
        title: "text-sm font-semibold",
        description: "text-xs text-[#5f6f8d]",
      },
    }}
    {...props}
  />
);

export { Toaster };
