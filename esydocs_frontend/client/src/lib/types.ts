export interface ToolConfig {
  id: string;
  name: string;
  description: string;
  icon: string;
  iconColor: string;
  bgColor: string;
  category: 'convert' | 'organize' | 'security';
}

export const TOOLS: ToolConfig[] = [
  // Convert Tools
  {
    id: 'pdf-to-word',
    name: 'PDF to Word',
    description: 'Convert PDF to editable Word documents',
    icon: 'fas fa-file-word',
    iconColor: 'text-red-600',
    bgColor: 'bg-red-100',
    category: 'convert'
  },
  {
    id: 'pdf-to-excel',
    name: 'PDF to Excel',
    description: 'Extract tables and data to spreadsheets',
    icon: 'fas fa-file-excel',
    iconColor: 'text-green-600',
    bgColor: 'bg-green-100',
    category: 'convert'
  },
  {
    id: 'pdf-to-powerpoint',
    name: 'PDF to PowerPoint',
    description: 'Convert PDF to presentation slides',
    icon: 'fas fa-file-powerpoint',
    iconColor: 'text-orange-600',
    bgColor: 'bg-orange-100',
    category: 'convert'
  },
  {
    id: 'word-to-pdf',
    name: 'Word to PDF',
    description: 'Convert Word documents to PDF',
    icon: 'fas fa-file-pdf',
    iconColor: 'text-blue-600',
    bgColor: 'bg-blue-100',
    category: 'convert'
  },
  
  // Organize Tools
  {
    id: 'merge-pdf',
    name: 'Merge PDF',
    description: 'Combine multiple PDF files into one',
    icon: 'fas fa-object-group',
    iconColor: 'text-purple-600',
    bgColor: 'bg-purple-100',
    category: 'organize'
  },
  {
    id: 'split-pdf',
    name: 'Split PDF',
    description: 'Extract pages from PDF files',
    icon: 'fas fa-cut',
    iconColor: 'text-pink-600',
    bgColor: 'bg-pink-100',
    category: 'organize'
  },
  {
    id: 'compress-pdf',
    name: 'Compress PDF',
    description: 'Reduce PDF file size efficiently',
    icon: 'fas fa-compress-arrows-alt',
    iconColor: 'text-teal-600',
    bgColor: 'bg-teal-100',
    category: 'organize'
  },
  {
    id: 'edit-pdf',
    name: 'Edit PDF',
    description: 'Add text, images, and annotations',
    icon: 'fas fa-edit',
    iconColor: 'text-indigo-600',
    bgColor: 'bg-indigo-100',
    category: 'organize'
  },
  
  // Security Tools
  {
    id: 'protect-pdf',
    name: 'Protect PDF',
    description: 'Add password protection to PDFs',
    icon: 'fas fa-lock',
    iconColor: 'text-red-600',
    bgColor: 'bg-red-100',
    category: 'security'
  },
  {
    id: 'unlock-pdf',
    name: 'Unlock PDF',
    description: 'Remove password from PDF files',
    icon: 'fas fa-unlock',
    iconColor: 'text-yellow-600',
    bgColor: 'bg-yellow-100',
    category: 'security'
  },
  {
    id: 'sign-pdf',
    name: 'Sign PDF',
    description: 'Add digital signatures to documents',
    icon: 'fas fa-signature',
    iconColor: 'text-green-600',
    bgColor: 'bg-green-100',
    category: 'security'
  },
  {
    id: 'watermark-pdf',
    name: 'Watermark',
    description: 'Add text or image watermarks',
    icon: 'fas fa-stamp',
    iconColor: 'text-blue-600',
    bgColor: 'bg-blue-100',
    category: 'security'
  }
];

export const TOOL_CATEGORIES = {
  convert: {
    title: 'Convert Documents',
    icon: 'fas fa-exchange-alt',
    description: 'Convert between different document formats'
  },
  organize: {
    title: 'Organize & Edit',
    icon: 'fas fa-layer-group',
    description: 'Merge, split, compress and edit your documents'
  },
  security: {
    title: 'Security & Protection',
    icon: 'fas fa-shield-alt',
    description: 'Protect and secure your documents'
  }
};
