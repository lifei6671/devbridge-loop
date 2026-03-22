import type { HTMLAttributes } from "react";

import { cn } from "../../lib/utils";

type DivProps = HTMLAttributes<HTMLDivElement>;

type HeadingProps = HTMLAttributes<HTMLHeadingElement>;

type ParagraphProps = HTMLAttributes<HTMLParagraphElement>;

export function Card({ children, className, ...props }: DivProps) {
  return (
    <div className={cn("ui-card", className)} {...props}>
      {children}
    </div>
  );
}

export function CardHeader({ children, className, ...props }: DivProps) {
  return (
    <div className={cn("ui-card-header", className)} {...props}>
      {children}
    </div>
  );
}

export function CardTitle({ children, className, ...props }: HeadingProps) {
  return (
    <h3 className={cn("ui-card-title", className)} {...props}>
      {children}
    </h3>
  );
}

export function CardDescription({ children, className, ...props }: ParagraphProps) {
  return (
    <p className={cn("ui-card-description", className)} {...props}>
      {children}
    </p>
  );
}

export function CardContent({ children, className, ...props }: DivProps) {
  return (
    <div className={cn("ui-card-content", className)} {...props}>
      {children}
    </div>
  );
}

export function CardFooter({ children, className, ...props }: DivProps) {
  return (
    <div className={cn("ui-card-footer", className)} {...props}>
      {children}
    </div>
  );
}
