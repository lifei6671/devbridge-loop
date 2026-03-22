import { cn } from "../../../lib/utils";

type LineNumberCodeBlockProps = {
  className?: string;
  code: string;
};

export function LineNumberCodeBlock(props: LineNumberCodeBlockProps) {
  const { className, code } = props;
  const normalizedCode = code.replace(/\r\n?/g, "\n");
  const lines = normalizedCode.split("\n");

  return (
    <ol className={cn("line-number-code-block", className)} aria-label="YAML 预览">
      {lines.map((line, index) => (
        <li key={`line-${index + 1}`} className="line-number-code-block-row">
          <span className="line-number-code-block-number" aria-hidden="true">
            {index + 1}
          </span>
          <code className="line-number-code-block-text">{line === "" ? " " : line}</code>
        </li>
      ))}
    </ol>
  );
}
