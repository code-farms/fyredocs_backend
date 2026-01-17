import { ToolDefinition, ToolId, ToolCategory } from '@/types';

/**
 * Tool configuration registry.
 * Each tool is fully defined here with its constraints and metadata.
 * Adding a new tool requires only adding an entry here and implementing its options component.
 */

const MB = 1024 * 1024;

export const TOOLS: Record<ToolId, ToolDefinition> = {
  merge: {
    id: 'merge',
    name: 'Merge PDF',
    description: 'Combine multiple PDF files into one document',
    category: 'merge',
    icon: 'layers',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 20,
    minFiles: 2,
    maxFileSize: 50 * MB,
    route: '/merge',
  },
  split: {
    id: 'split',
    name: 'Split PDF',
    description: 'Separate one PDF into multiple documents',
    category: 'split',
    icon: 'scissors',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 1,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/split',
  },
  compress: {
    id: 'compress',
    name: 'Compress PDF',
    description: 'Reduce file size while maintaining quality',
    category: 'compress',
    icon: 'minimize-2',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 10,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/compress',
  },
  'pdf-to-word': {
    id: 'pdf-to-word',
    name: 'PDF to Word',
    description: 'Convert PDF documents to editable Word files',
    category: 'convert',
    icon: 'file-text',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 5,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/pdf-to-word',
  },
  'word-to-pdf': {
    id: 'word-to-pdf',
    name: 'Word to PDF',
    description: 'Convert Word documents to PDF format',
    category: 'convert',
    icon: 'file',
    acceptedFileTypes: ['.doc', '.docx', 'application/msword', 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'],
    maxFiles: 5,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/word-to-pdf',
  },
  'pdf-to-excel': {
    id: 'pdf-to-excel',
    name: 'PDF to Excel',
    description: 'Extract tables from PDFs to Excel spreadsheets',
    category: 'convert',
    icon: 'table',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 5,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/pdf-to-excel',
  },
  'excel-to-pdf': {
    id: 'excel-to-pdf',
    name: 'Excel to PDF',
    description: 'Convert Excel spreadsheets to PDF format',
    category: 'convert',
    icon: 'file-spreadsheet',
    acceptedFileTypes: ['.xls', '.xlsx', 'application/vnd.ms-excel', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'],
    maxFiles: 5,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/excel-to-pdf',
  },
  'pdf-to-image': {
    id: 'pdf-to-image',
    name: 'PDF to Image',
    description: 'Convert PDF pages to JPG or PNG images',
    category: 'convert',
    icon: 'image',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 5,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/pdf-to-image',
  },
  'image-to-pdf': {
    id: 'image-to-pdf',
    name: 'Image to PDF',
    description: 'Convert images to a PDF document',
    category: 'convert',
    icon: 'file-image',
    acceptedFileTypes: ['.jpg', '.jpeg', '.png', '.gif', '.webp', 'image/jpeg', 'image/png', 'image/gif', 'image/webp'],
    maxFiles: 20,
    minFiles: 1,
    maxFileSize: 20 * MB,
    route: '/image-to-pdf',
  },
  reorder: {
    id: 'reorder',
    name: 'Reorder Pages',
    description: 'Rearrange pages within a PDF document',
    category: 'organize',
    icon: 'arrow-up-down',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 1,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/reorder',
  },
  rotate: {
    id: 'rotate',
    name: 'Rotate Pages',
    description: 'Rotate PDF pages to the correct orientation',
    category: 'organize',
    icon: 'rotate-cw',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 1,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/rotate',
  },
  ocr: {
    id: 'ocr',
    name: 'OCR PDF',
    description: 'Extract text from scanned documents',
    category: 'ocr',
    icon: 'scan-text',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 5,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/ocr',
  },
  watermark: {
    id: 'watermark',
    name: 'Add Watermark',
    description: 'Add text or image watermarks to PDFs',
    category: 'watermark',
    icon: 'stamp',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 10,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/watermark',
  },
  'password-protect': {
    id: 'password-protect',
    name: 'Protect PDF',
    description: 'Add password protection to PDF documents',
    category: 'security',
    icon: 'lock',
    acceptedFileTypes: ['.pdf', 'application/pdf'],
    maxFiles: 10,
    minFiles: 1,
    maxFileSize: 50 * MB,
    route: '/protect',
  },
};

export const TOOL_CATEGORIES: Record<ToolCategory, { name: string; color: string }> = {
  merge: { name: 'Merge & Combine', color: 'tool-merge' },
  split: { name: 'Split & Extract', color: 'tool-split' },
  compress: { name: 'Compress', color: 'tool-compress' },
  convert: { name: 'Convert', color: 'tool-convert' },
  organize: { name: 'Organize', color: 'tool-organize' },
  security: { name: 'Security', color: 'tool-security' },
  ocr: { name: 'OCR', color: 'tool-ocr' },
  watermark: { name: 'Watermark', color: 'tool-watermark' },
};

export const getToolsByCategory = (category: ToolCategory): ToolDefinition[] => {
  return Object.values(TOOLS).filter((tool) => tool.category === category);
};

export const getAllTools = (): ToolDefinition[] => {
  return Object.values(TOOLS);
};

export const getToolById = (id: ToolId): ToolDefinition | undefined => {
  return TOOLS[id];
};
