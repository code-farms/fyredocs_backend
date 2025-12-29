const CONVERT_FROM_PDF_BASE = "/api/convert-from-pdf";
const CONVERT_TO_PDF_BASE = "/api/convert-to-pdf";

const CONVERT_FROM_PDF_TOOLS = new Set([
  "pdf-to-word",
  "pdf-to-excel",
  "pdf-to-powerpoint",
  "pdf-to-ppt",
  "pdf-to-image",
  "pdf-to-img",
  "merge-pdf",
  "split-pdf",
  "compress-pdf",
  "edit-pdf",
  "protect-pdf",
  "unlock-pdf",
  "sign-pdf",
  "watermark-pdf",
]);

const CONVERT_TO_PDF_TOOLS = new Set([
  "word-to-pdf",
  "ppt-to-pdf",
  "powerpoint-to-pdf",
  "excel-to-pdf",
  "image-to-pdf",
  "img-to-pdf",
]);

export function normalizeToolType(toolType: string): string {
  if (toolType === "powerpoint-to-pdf") {
    return "ppt-to-pdf";
  }
  return toolType;
}

export function getServiceBasePath(toolType: string): string {
  const normalizedTool = normalizeToolType(toolType);
  if (CONVERT_TO_PDF_TOOLS.has(normalizedTool)) {
    return CONVERT_TO_PDF_BASE;
  }
  if (CONVERT_FROM_PDF_TOOLS.has(normalizedTool)) {
    return CONVERT_FROM_PDF_BASE;
  }
  return CONVERT_FROM_PDF_BASE;
}

export function getAllJobServiceBases(): string[] {
  return [CONVERT_FROM_PDF_BASE, CONVERT_TO_PDF_BASE];
}
