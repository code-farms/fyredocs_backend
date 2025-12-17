import type { Express, Request } from "express";
import { createServer, type Server } from "http";
import { storage } from "./storage";
import { insertProcessingJobSchema } from "@shared/schema";
import multer from "multer";
import path from "path";
import fs from "fs";
import { promises as fsPromises } from "fs";
import ConvertAPI from 'convertapi';
import { Document, Packer, Paragraph, TextRun } from "docx";

const convertapi = new ConvertAPI(process.env.CONVERT_API_SECRET as string);

// Extend Express Request type to include multer file
interface MulterRequest extends Request {
  files?: Express.Multer.File[];
}

const upload = multer({
  dest: "uploads/",
  limits: { fileSize: 50 * 1024 * 1024 } // 50MB limit
});

// Ensure output directory exists
const outputDir = "outputs";
if (!fs.existsSync(outputDir)) {
  fs.mkdirSync(outputDir, { recursive: true });
}

async function convertPdfToWord(inputPath: string, outputPath: string): Promise<void> {
  try {
    console.log(`Converting PDF to Word: ${inputPath} -> ${outputPath}`);
    
    // Read the uploaded file info for demonstration
    const stats = await fsPromises.stat(inputPath);
    const fileSize = (stats.size / 1024).toFixed(2);
    
    console.log(`Input file size: ${fileSize} KB`);
    
    // For now, create a sample Word document with placeholder content
    // In a real implementation, you would use a proper PDF parsing library
    const sampleText = `Document Conversion Results
    
Original PDF File: ${path.basename(inputPath)}
File Size: ${fileSize} KB
Conversion Date: ${new Date().toLocaleDateString()}
Conversion Time: ${new Date().toLocaleTimeString()}

This is a converted document from PDF to Word format.

Note: This is a demonstration conversion. In a production environment, 
this would contain the actual extracted text from your PDF file.

The DocuFlow platform supports various document processing tools:
- PDF to Word conversion
- PDF to Excel conversion  
- PDF to PowerPoint conversion
- Merge multiple PDFs
- Split PDF pages
- Compress PDF files
- Add password protection
- Digital signatures
- Watermarks

Your document has been successfully processed using our secure cloud infrastructure.`;

    // Split text into paragraphs
    const paragraphs = sampleText.split('\n').filter((line: string) => line.trim().length > 0);
    
    // Create Word document
    const doc = new Document({
      sections: [{
        properties: {},
        children: paragraphs.map((paragraph: string) => 
          new Paragraph({
            children: [new TextRun(paragraph.trim())],
          })
        ),
      }],
    });
    
    // Generate Word document buffer
    const buffer = await Packer.toBuffer(doc);
    
    // Save to file
    await fsPromises.writeFile(outputPath, buffer);
    console.log(`Word document created successfully: ${outputPath}`);
    
    // Verify the file was created
    const outputStats = await fsPromises.stat(outputPath);
    console.log(`Output file size: ${(outputStats.size / 1024).toFixed(2)} KB`);
  } catch (error) {
    console.error('PDF to Word conversion error:', error);
    throw new Error('Failed to convert PDF to Word');
  }
}

async function convertPdfToExcel(inputPath: string, outputPath: string): Promise<void> {
  try {
    console.log(`Converting PDF to Excel: ${inputPath} -> ${outputPath}`);
    const result = await convertapi.convert('xlsx', { File: inputPath });
    await result.saveFiles(outputPath);
    console.log(`Excel document created successfully: ${outputPath}`);
  } catch (error) {
    console.error('PDF to Excel conversion error:', error);
    throw new Error('Failed to convert PDF to Excel');
  }
}

async function convertPdfToPowerpoint(inputPath: string, outputPath: string): Promise<void> {
  try {
    console.log(`Converting PDF to Powerpoint: ${inputPath} -> ${outputPath}`);
    const result = await convertapi.convert('pptx', { File: inputPath });
    await result.saveFiles(outputPath);
    console.log(`Powerpoint document created successfully: ${outputPath}`);
  } catch (error) {
    console.error('PDF to Powerpoint conversion error:', error);
    throw new Error('Failed to convert PDF to Powerpoint');
  }
}

async function convertWordToPdf(inputPath: string, outputPath: string): Promise<void> {
  try {
    console.log(`Converting Word to PDF: ${inputPath} -> ${outputPath}`);
    const result = await convertapi.convert('pdf', { File: inputPath });
    await result.saveFiles(outputPath);
    console.log(`PDF document created successfully: ${outputPath}`);
  } catch (error) {
    console.error('Word to PDF conversion error:', error);
    throw new Error('Failed to convert Word to PDF');
  }
}

async function compressPdf(inputPath: string, outputPath: string): Promise<void> {
  try {
    console.log(`Compressing PDF: ${inputPath} -> ${outputPath}`);
    const result = await convertapi.convert('pdf', { File: inputPath }, 'compress');
    await result.saveFiles(outputPath);
    console.log(`PDF document compressed successfully: ${outputPath}`);
  } catch (error) {
    console.error('PDF compression error:', error);
    throw new Error('Failed to compress PDF');
  }
}

async function mergePdf(inputPaths: string[], outputPath: string): Promise<void> {
  try {
    console.log(`Merging PDFs: ${inputPaths.join(", ")} -> ${outputPath}`);
    const result = await convertapi.convert('pdf', { Files: inputPaths }, 'merge');
    await result.saveFiles(outputPath);
    console.log(`PDF document merged successfully: ${outputPath}`);
  } catch (error) {
    console.error('PDF merge error:', error);
    throw new Error('Failed to merge PDFs');
  }
}

async function splitPdf(inputPath: string, outputPath: string, options: any): Promise<void> {
  try {
    console.log(`Splitting PDF: ${inputPath} -> ${outputPath}`);
    const params = {
      File: inputPath,
      PageRange: options.range
    }
    const result = await convertapi.convert('pdf', params, 'split');
    await result.saveFiles(outputPath);
    console.log(`PDF document split successfully: ${outputPath}`);
  } catch (error) {
    console.error('PDF split error:', error);
    throw new Error('Failed to split PDF');
  }
}

async function protectPdf(inputPath: string, outputPath: string, options: any): Promise<void> {
  try {
    console.log(`Protecting PDF: ${inputPath} -> ${outputPath}`);
    const params = {
      File: inputPath,
      UserPassword: options.password
    }
    const result = await convertapi.convert('pdf', params, 'encrypt');
    await result.saveFiles(outputPath);
    console.log(`PDF document protected successfully: ${outputPath}`);
  } catch (error) {
    console.error('PDF protect error:', error);
    throw new Error('Failed to protect PDF');
  }
}

async function processFile(jobId: string, toolType: string, inputPath: string | string[], options: any): Promise<string> {
  const outputFileName = `processed_${jobId}_${Date.now()}`;
  let outputPath: string;
  
  console.log(`Processing file for job ${jobId}, tool: ${toolType}, input: ${inputPath}`);
  
  try {
    switch (toolType) {
      case 'pdf-to-word':
        outputPath = path.join(outputDir, `${outputFileName}.docx`);
        console.log(`Will create Word document at: ${outputPath}`);
        await convertPdfToWord(inputPath as string, outputPath);
        break;
      
      case 'pdf-to-excel':
        outputPath = path.join(outputDir, `${outputFileName}.xlsx`);
        console.log(`Will create Excel document at: ${outputPath}`);
        await convertPdfToExcel(inputPath as string, outputPath);
        break;

      case 'pdf-to-powerpoint':
        outputPath = path.join(outputDir, `${outputFileName}.pptx`);
        console.log(`Will create Powerpoint document at: ${outputPath}`);
        await convertPdfToPowerpoint(inputPath as string, outputPath);
        break;

      case 'word-to-pdf':
        outputPath = path.join(outputDir, `${outputFileName}.pdf`);
        console.log(`Will create PDF document at: ${outputPath}`);
        await convertWordToPdf(inputPath as string, outputPath);
        break;

      case 'compress-pdf':
        outputPath = path.join(outputDir, `${outputFileName}.pdf`);
        console.log(`Will create compressed PDF document at: ${outputPath}`);
        await compressPdf(inputPath as string, outputPath);
        break;

      case 'merge-pdf':
        outputPath = path.join(outputDir, `${outputFileName}.pdf`);
        console.log(`Will create merged PDF document at: ${outputPath}`);
        await mergePdf(inputPath as string[], outputPath);
        break;

      case 'split-pdf':
        outputPath = path.join(outputDir, `${outputFileName}.zip`);
        console.log(`Will create split PDF zip at: ${outputPath}`);
        await splitPdf(inputPath as string, outputPath, options);
        break;

      case 'protect-pdf':
        outputPath = path.join(outputDir, `${outputFileName}.pdf`);
        console.log(`Will create protected PDF at: ${outputPath}`);
        await protectPdf(inputPath as string, outputPath, options);
        break;

      case 'edit-pdf':
        // Placeholder for edit-pdf
        outputPath = path.join(outputDir, `${outputFileName}.pdf`);
        await fsPromises.copyFile(inputPath as string, outputPath);
        break;

      case 'unlock-pdf':
        // Placeholder for unlock-pdf
        outputPath = path.join(outputDir, `${outputFileName}.pdf`);
        await fsPromises.copyFile(inputPath as string, outputPath);
        break;

      case 'sign-pdf':
        // Placeholder for sign-pdf
        outputPath = path.join(outputDir, `${outputFileName}.pdf`);
        await fsPromises.copyFile(inputPath as string, outputPath);
        break;

      case 'watermark-pdf':
        // Placeholder for watermark-pdf
        outputPath = path.join(outputDir, `${outputFileName}.pdf`);
        await fsPromises.copyFile(inputPath as string, outputPath);
        break;
        
      default:
        throw new Error(`Unsupported tool type: ${toolType}`);
    }
    
    return outputPath;
  } catch (error) {
    console.error(`Processing error for ${toolType}:`, error);
    throw error;
  }
}

export async function registerRoutes(app: Express): Promise<Server> {
  // Get all processing jobs
  app.get("/api/jobs", async (req, res) => {
    try {
      const jobs = await storage.getProcessingJobsByUser();
      res.json(jobs);
    } catch (error) {
      res.status(500).json({ message: "Failed to fetch jobs" });
    }
  });

  // Get specific processing job
  app.get("/api/jobs/:id", async (req, res) => {
    try {
      const job = await storage.getProcessingJob(req.params.id);
      if (!job) {
        return res.status(404).json({ message: "Job not found" });
      }
      res.json(job);
    } catch (error) {
      res.status(500).json({ message: "Failed to fetch job" });
    }
  });

  // Create new processing job with file upload
  app.post("/api/jobs", upload.array("files"), async (req: MulterRequest, res) => {
    try {
      if (!req.files || req.files.length === 0) {
        return res.status(400).json({ message: "No file uploaded" });
      }

      const { toolType, options } = req.body;
      
      if (!toolType) {
        return res.status(400).json({ message: "Tool type is required" });
      }

      const files = req.files as Express.Multer.File[];
      const file = files[0];

      const jobData = {
        fileName: toolType === 'merge-pdf' ? 'merged.pdf' : file.originalname,
        fileSize: files.reduce((acc, file) => acc + file.size, 0) / 1024 + " KB",
        toolType,
        metadata: options ? JSON.parse(options) : null,
      };

      const validatedData = insertProcessingJobSchema.parse(jobData);
      const metadata = validatedData.metadata || {};
      const jobInput = {
        fileName: validatedData.fileName,
        fileSize: validatedData.fileSize,
        toolType: validatedData.toolType,
        metadata: { ...metadata, inputFilePath: files.map(f => f.path) }
      };
      const job = await storage.createProcessingJob(jobInput);

      // Start actual file processing
      setTimeout(async () => {
        try {
          const inputPaths = files.map(f => f.path);
          console.log(`Starting processing for job ${job.id}, tool: ${toolType}, files: ${inputPaths.join(", ")}`);
          
          await storage.updateProcessingJob(job.id, { 
            status: "processing", 
            progress: "25" 
          });
          
          // Perform actual conversion
          const outputPath = await processFile(job.id, toolType, toolType === 'merge-pdf' ? inputPaths : inputPaths[0], options ? JSON.parse(options) : {});
          console.log(`Conversion completed, output file: ${outputPath}`);
          
          await storage.updateProcessingJob(job.id, { 
            progress: "75" 
          });
          
          await storage.updateProcessingJob(job.id, { 
            status: "completed", 
            progress: "100",
            completedAt: new Date(),
            outputFileUrl: `/api/download/${job.id}`,
            metadata: { 
              ...(job.metadata || {}), 
              inputFilePath: inputPaths,
              outputFilePath: outputPath 
            }
          });
          
          console.log(`Job ${job.id} completed successfully`);
        } catch (error) {
          console.error('Processing failed:', error);
          await storage.updateProcessingJob(job.id, { 
            status: "failed", 
            progress: "0"
          });
        }
      }, 1000);

      res.status(201).json(job);
    } catch (error) {
      res.status(400).json({ message: error instanceof Error ? error.message : "Invalid job data" });
    }
  });

  // Update processing job status/progress
  app.patch("/api/jobs/:id", async (req, res) => {
    try {
      const { status, progress } = req.body;
      const job = await storage.updateProcessingJob(req.params.id, {
        status,
        progress,
        ...(status === "completed" && { completedAt: new Date() })
      });
      
      if (!job) {
        return res.status(404).json({ message: "Job not found" });
      }
      
      res.json(job);
    } catch (error) {
      res.status(500).json({ message: "Failed to update job" });
    }
  });

  // Download processed file
  app.get("/api/download/:id", async (req, res) => {
    try {
      console.log(`Download request for job: ${req.params.id}`);
      const job = await storage.getProcessingJob(req.params.id);
      if (!job || job.status !== "completed") {
        console.log(`Job not found or not completed: ${job?.status}`);
        return res.status(404).json({ message: "File not ready for download" });
      }

      const outputFilePath = (job.metadata as any)?.outputFilePath as string;
      console.log(`Looking for output file: ${outputFilePath}`);
      if (!outputFilePath || !fs.existsSync(outputFilePath)) {
        console.log(`Output file not found or doesn't exist`);
        return res.status(404).json({ message: "Processed file not found" });
      }

      // Determine file extension and content type based on tool type
      let fileName = `processed_${job.fileName}`;
      let contentType = 'application/octet-stream';
      
      switch (job.toolType) {
        case 'pdf-to-word':
          fileName = fileName.replace('.pdf', '.docx');
          contentType = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document';
          break;
        case 'pdf-to-excel':
          fileName = fileName.replace('.pdf', '.xlsx');
          contentType = 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet';
          break;
        case 'pdf-to-powerpoint':
          fileName = fileName.replace('.pdf', '.pptx');
          contentType = 'application/vnd.openxmlformats-officedocument.presentationml.presentation';
          break;
        case 'split-pdf':
          fileName = fileName.replace('.pdf', '.zip');
          contentType = 'application/zip';
          break;
        default:
          contentType = 'application/pdf';
      }
      
      console.log(`Serving file: ${fileName} with content type: ${contentType}`);
      
      res.setHeader('Content-Disposition', `attachment; filename="${fileName}"`);
      res.setHeader('Content-Type', contentType);
      
      // Stream the file
      const fileStream = fs.createReadStream(outputFilePath);
      fileStream.pipe(res);
      
      // Clean up files after download (keep for 1 hour)
      fileStream.on('end', () => {
        setTimeout(() => {
          try {
            console.log(`Cleaning up files for job ${job.id}`);
            if (fs.existsSync(outputFilePath)) {
              fs.unlinkSync(outputFilePath);
              console.log(`Deleted output file: ${outputFilePath}`);
            }
            const inputFilePath = (job.metadata as any)?.inputFilePath as string;
            if (inputFilePath && fs.existsSync(inputFilePath)) {
              fs.unlinkSync(inputFilePath);
              console.log(`Deleted input file: ${inputFilePath}`);
            }
          } catch (cleanupError) {
            console.error('File cleanup error:', cleanupError);
          }
        }, 3600000); // Clean up after 1 hour (3600 seconds)
      });
      
    } catch (error) {
      console.error('Download error:', error);
      res.status(500).json({ message: "Download failed" });
    }
  });

  // Delete processing job
  app.delete("/api/jobs/:id", async (req, res) => {
    try {
      const deleted = await storage.deleteProcessingJob(req.params.id);
      if (!deleted) {
        return res.status(404).json({ message: "Job not found" });
      }
      res.status(204).send();
    } catch (error) {
      res.status(500).json({ message: "Failed to delete job" });
    }
  });

  const httpServer = createServer(app);
  return httpServer;
}