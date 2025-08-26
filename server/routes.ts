import type { Express, Request } from "express";
import { createServer, type Server } from "http";
import { storage } from "./storage";
import { insertProcessingJobSchema } from "@shared/schema";
import multer from "multer";
import path from "path";
import fs from "fs";

// Extend Express Request type to include multer file
interface MulterRequest extends Request {
  file?: Express.Multer.File;
}

const upload = multer({ 
  dest: "uploads/",
  limits: { fileSize: 50 * 1024 * 1024 } // 50MB limit
});

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
      const job = await storage.createProcessingJob(validatedData);

      // Simulate processing with mock delay
      setTimeout(async () => {
        await storage.updateProcessingJob(job.id, { 
          status: "processing", 
          progress: "25" 
        });
        
        setTimeout(async () => {
          await storage.updateProcessingJob(job.id, { 
            progress: "75" 
          });
          
          setTimeout(async () => {
            await storage.updateProcessingJob(job.id, { 
              status: "completed", 
              progress: "100",
              completedAt: new Date(),
              outputFileUrl: `/api/download/${job.id}`,
            });
          }, 1000);
        }, 1500);
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

  // Download processed file (mock implementation)
  app.get("/api/download/:id", async (req, res) => {
    try {
      const job = await storage.getProcessingJob(req.params.id);
      if (!job || job.status !== "completed") {
        return res.status(404).json({ message: "File not ready for download" });
      }

      // Mock file download - in real implementation, this would serve the actual processed file
      const mockFileContent = `Processed file: ${job.fileName}\nTool used: ${job.toolType}\nProcessed at: ${job.completedAt}`;
      
      res.setHeader('Content-Disposition', `attachment; filename="processed_${job.fileName}"`);
      res.setHeader('Content-Type', 'application/octet-stream');
      res.send(mockFileContent);
    } catch (error) {
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
