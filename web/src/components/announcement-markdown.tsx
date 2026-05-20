"use client";

import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

type AnnouncementMarkdownProps = {
  children: string;
  className?: string;
  compact?: boolean;
};

type MarkdownBlock =
  | {
      type: "heading";
      level: number;
      text: string;
    }
  | {
      type: "list";
      ordered: boolean;
      items: string[];
    }
  | {
      type: "paragraph";
      lines: string[];
    };

const BARE_LINK_PATTERN = /^(https?:\/\/[^\s<>"']+|mailto:[^\s<>"']+|tel:[^\s<>"']+)/i;
const TRAILING_URL_PUNCTUATION_PATTERN = /[),.;:!?，。！？；：、]+$/;
const LINK_CLASS_NAME =
  "font-medium text-[#1456f0] underline underline-offset-2 transition hover:text-[#17437d] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#1456f0]/25";

export function AnnouncementMarkdown({
  children,
  className,
  compact = false,
}: AnnouncementMarkdownProps) {
  const content = children.trim();

  if (!content) {
    return null;
  }

  if (compact) {
    return (
      <div className={cn("break-words whitespace-pre-wrap", className)}>
        {renderInline(content, "compact")}
      </div>
    );
  }

  return (
    <div className={cn("space-y-2 break-words text-sm leading-6", className)}>
      {parseBlocks(content).map((block, index) => renderBlock(block, `block-${index}`))}
    </div>
  );
}

function parseBlocks(content: string): MarkdownBlock[] {
  const lines = content.replace(/\r\n?/g, "\n").split("\n");
  const blocks: MarkdownBlock[] = [];
  let index = 0;

  while (index < lines.length) {
    if (!lines[index].trim()) {
      index += 1;
      continue;
    }

    const heading = parseHeading(lines[index]);
    if (heading) {
      blocks.push(heading);
      index += 1;
      continue;
    }

    const listItem = parseListItem(lines[index]);
    if (listItem) {
      const items: string[] = [];
      const ordered = listItem.ordered;

      while (index < lines.length) {
        const item = parseListItem(lines[index]);
        if (!item || item.ordered !== ordered) {
          break;
        }
        items.push(item.text);
        index += 1;
      }

      blocks.push({ type: "list", ordered, items });
      continue;
    }

    const paragraphLines: string[] = [];
    while (index < lines.length && lines[index].trim()) {
      if (paragraphLines.length > 0 && (parseHeading(lines[index]) || parseListItem(lines[index]))) {
        break;
      }
      paragraphLines.push(lines[index]);
      index += 1;
    }
    blocks.push({ type: "paragraph", lines: paragraphLines });
  }

  return blocks;
}

function parseHeading(line: string): MarkdownBlock | null {
  const match = /^\s{0,3}(#{1,3})\s+(.+?)\s*#*\s*$/.exec(line);
  if (!match) {
    return null;
  }
  return {
    type: "heading",
    level: match[1].length,
    text: match[2],
  };
}

function parseListItem(line: string): { ordered: boolean; text: string } | null {
  const unordered = /^\s{0,3}[-*+]\s+(.+)$/.exec(line);
  if (unordered) {
    return { ordered: false, text: unordered[1] };
  }

  const ordered = /^\s{0,3}\d+[.)]\s+(.+)$/.exec(line);
  if (ordered) {
    return { ordered: true, text: ordered[1] };
  }

  return null;
}

function renderBlock(block: MarkdownBlock, key: string) {
  if (block.type === "heading") {
    const className = cn(
      "font-semibold text-stone-900",
      block.level === 1 ? "text-base" : "text-sm",
    );
    return (
      <div key={key} className={className}>
        {renderInline(block.text, key)}
      </div>
    );
  }

  if (block.type === "list") {
    const ListTag = block.ordered ? "ol" : "ul";
    return (
      <ListTag
        key={key}
        className={cn(
          "space-y-1 pl-5",
          block.ordered ? "list-decimal" : "list-disc",
        )}
      >
        {block.items.map((item, index) => (
          <li key={`${key}-item-${index}`}>{renderInline(item, `${key}-item-${index}`)}</li>
        ))}
      </ListTag>
    );
  }

  return (
    <p key={key} className="whitespace-pre-wrap">
      {renderInlineLines(block.lines, key)}
    </p>
  );
}

function renderInlineLines(lines: string[], keyPrefix: string) {
  return lines.flatMap((line, index) => {
    const nodes = renderInline(line, `${keyPrefix}-line-${index}`);
    if (index === lines.length - 1) {
      return nodes;
    }
    return [...nodes, <br key={`${keyPrefix}-line-${index}-break`} />];
  });
}

function renderInline(text: string, keyPrefix: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  let cursor = 0;
  let textBuffer = "";

  const flushText = () => {
    if (!textBuffer) {
      return;
    }
    nodes.push(textBuffer);
    textBuffer = "";
  };

  const pushNode = (node: ReactNode) => {
    flushText();
    nodes.push(node);
  };

  while (cursor < text.length) {
    const char = text[cursor];

    if (char === "`") {
      const end = text.indexOf("`", cursor + 1);
      if (end > cursor + 1) {
        pushNode(
          <code
            key={`${keyPrefix}-code-${cursor}`}
            className="rounded-md bg-amber-100/70 px-1.5 py-0.5 font-mono text-[0.9em] text-stone-800"
          >
            {text.slice(cursor + 1, end)}
          </code>,
        );
        cursor = end + 1;
        continue;
      }
    }

    if (text.startsWith("**", cursor)) {
      const end = text.indexOf("**", cursor + 2);
      if (end > cursor + 2) {
        pushNode(
          <strong key={`${keyPrefix}-strong-${cursor}`} className="font-semibold text-stone-900">
            {renderInline(text.slice(cursor + 2, end), `${keyPrefix}-strong-${cursor}`)}
          </strong>,
        );
        cursor = end + 2;
        continue;
      }
    }

    const markdownLink = parseMarkdownLink(text, cursor);
    if (markdownLink) {
      pushNode(
        <AnnouncementLink key={`${keyPrefix}-link-${cursor}`} href={markdownLink.href}>
          {renderInline(markdownLink.label, `${keyPrefix}-link-${cursor}-label`)}
        </AnnouncementLink>,
      );
      cursor = markdownLink.end;
      continue;
    }

    const autoLink = parseAutoLink(text, cursor);
    if (autoLink) {
      pushNode(
        <AnnouncementLink key={`${keyPrefix}-autolink-${cursor}`} href={autoLink.href}>
          {autoLink.href}
        </AnnouncementLink>,
      );
      cursor = autoLink.end;
      continue;
    }

    const bareLink = parseBareLink(text, cursor);
    if (bareLink) {
      pushNode(
        <AnnouncementLink key={`${keyPrefix}-barelink-${cursor}`} href={bareLink.href}>
          {bareLink.href}
        </AnnouncementLink>,
      );
      if (bareLink.trailing) {
        textBuffer += bareLink.trailing;
      }
      cursor = bareLink.end;
      continue;
    }

    textBuffer += char;
    cursor += 1;
  }

  flushText();
  return nodes;
}

function parseMarkdownLink(text: string, start: number) {
  if (text[start] !== "[" || text[start - 1] === "!") {
    return null;
  }

  const labelEnd = text.indexOf("]", start + 1);
  if (labelEnd <= start + 1 || text[labelEnd + 1] !== "(") {
    return null;
  }

  const hrefStart = labelEnd + 2;
  const hrefEnd = findClosingParen(text, hrefStart);
  if (hrefEnd <= hrefStart) {
    return null;
  }

  const href = sanitizeHref(text.slice(hrefStart, hrefEnd));
  if (!href) {
    return null;
  }

  return {
    label: text.slice(start + 1, labelEnd),
    href,
    end: hrefEnd + 1,
  };
}

function findClosingParen(text: string, start: number) {
  let depth = 0;

  for (let index = start; index < text.length; index += 1) {
    const char = text[index];
    if (char === "(") {
      depth += 1;
      continue;
    }
    if (char !== ")") {
      continue;
    }
    if (depth === 0) {
      return index;
    }
    depth -= 1;
  }

  return -1;
}

function parseAutoLink(text: string, start: number) {
  if (text[start] !== "<") {
    return null;
  }

  const end = text.indexOf(">", start + 1);
  if (end <= start + 1) {
    return null;
  }

  const href = sanitizeHref(text.slice(start + 1, end));
  if (!href) {
    return null;
  }

  return { href, end: end + 1 };
}

function parseBareLink(text: string, start: number) {
  const match = BARE_LINK_PATTERN.exec(text.slice(start));
  if (!match) {
    return null;
  }

  const rawHref = match[0];
  const href = sanitizeHref(rawHref.replace(TRAILING_URL_PUNCTUATION_PATTERN, ""));
  if (!href) {
    return null;
  }

  return {
    href,
    trailing: rawHref.slice(href.length),
    end: start + rawHref.length,
  };
}

function sanitizeHref(rawHref: string) {
  const href = rawHref.trim().replace(/^<|>$/g, "");
  if (!href || hasControlCharacter(href)) {
    return "";
  }

  if (href.startsWith("#") || (href.startsWith("/") && !href.startsWith("//"))) {
    return href;
  }

  try {
    const url = new URL(href);
    if (url.protocol === "http:" || url.protocol === "https:" || url.protocol === "mailto:" || url.protocol === "tel:") {
      return href;
    }
  } catch {
    return "";
  }

  return "";
}

function hasControlCharacter(value: string) {
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (code <= 31 || code === 127) {
      return true;
    }
  }
  return false;
}

function AnnouncementLink({
  href,
  children,
}: {
  href: string;
  children: ReactNode;
}) {
  const external = /^https?:\/\//i.test(href);

  return (
    <a
      className={LINK_CLASS_NAME}
      href={href}
      target={external ? "_blank" : undefined}
      rel={external ? "noopener noreferrer" : undefined}
    >
      {children}
    </a>
  );
}
