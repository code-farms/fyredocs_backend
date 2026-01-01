const CONVERT_FROM_PDF_BASE = "/api/convert-from-pdf";
const CONVERT_TO_PDF_BASE = "/api/convert-to-pdf";

const CONVERT_FROM_PDF_TOOLS = new Set([
  "pdf-to-word",
  "pdf-to-excel",
  "pdf-to-powerpoint",
  "pdf-to-image",
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
  "powerpoint-to-pdf",
  "excel-to-pdf",
  "image-to-pdf",
]);

export function normalizeToolType(toolType: string): string {
  if (toolType === "ppt-to-pdf") {
    return "powerpoint-to-pdf";
  }
  if (toolType === "pdf-to-ppt") {
    return "pdf-to-powerpoint";
  }
  if (toolType === "pdf-to-img") {
    return "pdf-to-image";
  }
  if (toolType === "img-to-pdf") {
    return "image-to-pdf";
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

export function getToolEndpoint(toolType: string): string {
  const normalizedTool = normalizeToolType(toolType);
  const basePath = getServiceBasePath(normalizedTool);
  return `${basePath}/${normalizedTool}`;
}

export function getAllToolEndpoints(): string[] {
  const fromTools = Array.from(CONVERT_FROM_PDF_TOOLS);
  const toTools = Array.from(CONVERT_TO_PDF_TOOLS);

  return [
    ...fromTools.map((tool) => `${CONVERT_FROM_PDF_BASE}/${normalizeToolType(tool)}`),
    ...toTools.map((tool) => `${CONVERT_TO_PDF_BASE}/${normalizeToolType(tool)}`),
  ];
}
