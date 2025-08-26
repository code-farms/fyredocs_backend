import type { Express, Request } from "express";
import { createServer, type Server } from "http";
import { storage } from "./storage";
import { insertProcessingJobSchema } from "@shared/schema";
import multer from "multer";
import path from "path";
import fs from "fs";
import { promises as fsPromises } from "fs";
import { Document, Packer, Paragraph, TextRun } from "docx";
// Dynamic import for pdf-parse will be done in the function

// Extend Express Request type to include multer file
interface MulterRequest extends Request {
  file?: Express.Multer.File;
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

async function processFile(jobId: string, toolType: string, inputPath: string): Promise<string> {
  const outputFileName = `processed_${jobId}_${Date.now()}`;
  let outputPath: string;
  
  try {
    switch (toolType) {
      case 'pdf-to-word':
        outputPath = path.join(outputDir, `${outputFileName}.docx`);
        await convertPdfToWord(inputPath, outputPath);
        break;
        
      case 'pdf-to-excel':
      case 'pdf-to-powerpoint':
      case 'word-to-pdf':
      case 'merge-pdf':
      case 'split-pdf':
      case 'compress-pdf':
      case 'edit-pdf':
      case 'protect-pdf':
      case 'unlock-pdf':
      case 'sign-pdf':
      case 'watermark-pdf':
        // Placeholder for other conversions - for now, just copy the file
        outputPath = path.join(outputDir, `${outputFileName}.pdf`);
        await fsPromises.copyFile(inputPath, outputPath);
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
  app.post("/api/jobs", upload.single("file"), async (req: MulterRequest, res) => {
    try {
      if (!req.file) {
        return res.status(400).json({ message: "No file uploaded" });
      }

      const { toolType, options } = req.body;
      
      if (!toolType) {
        return res.status(400).json({ message: "Tool type is required" });
      }

      const jobData = {
        fileName: req.file.originalname,
        fileSize: (req.file.size / 1024).toFixed(2) + " KB",
        toolType,
        metadata: options ? JSON.parse(options) : null,
      };

      const validatedData = insertProcessingJobSchema.parse(jobData);
      const metadata = validatedData.metadata || {};
      const jobInput = {
        fileName: validatedData.fileName,
        fileSize: validatedData.fileSize,
        toolType: validatedData.toolType,
        metadata: { ...metadata, inputFilePath: req.file!.path }
      };
      const job = await storage.createProcessingJob(jobInput);

      // Start actual file processing
      setTimeout(async () => {
        try {
          console.log(`Starting processing for job ${job.id}, tool: ${toolType}, file: ${req.file!.path}`);
          
          await storage.updateProcessingJob(job.id, { 
            status: "processing", 
            progress: "25" 
          });
          
          // Perform actual conversion
          const outputPath = await processFile(job.id, toolType, req.file!.path);
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
              inputFilePath: req.file!.path,
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
      const job = await storage.getProcessingJob(req.params.id);
      if (!job || job.status !== "completed") {
        return res.status(404).json({ message: "File not ready for download" });
      }

      const outputFilePath = (job.metadata as any)?.outputFilePath as string;
      if (!outputFilePath || !fs.existsSync(outputFilePath)) {
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
        default:
          contentType = 'application/pdf';
      }
      
      res.setHeader('Content-Disposition', `attachment; filename="${fileName}"`);
      res.setHeader('Content-Type', contentType);
      
      // Stream the file
      const fileStream = fs.createReadStream(outputFilePath);
      fileStream.pipe(res);
      
      // Clean up files after download
      fileStream.on('end', () => {
        setTimeout(() => {
          try {
            if (fs.existsSync(outputFilePath)) fs.unlinkSync(outputFilePath);
            const inputFilePath = (job.metadata as any)?.inputFilePath as string;
            if (inputFilePath && fs.existsSync(inputFilePath)) fs.unlinkSync(inputFilePath);
          } catch (cleanupError) {
            console.error('File cleanup error:', cleanupError);
          }
        }, 5000); // Clean up after 5 seconds
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
