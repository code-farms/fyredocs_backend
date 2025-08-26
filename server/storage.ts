import { type User, type InsertUser, type ProcessingJob, type InsertProcessingJob } from "@shared/schema";
import { randomUUID } from "crypto";

export interface IStorage {
  getUser(id: string): Promise<User | undefined>;
  getUserByUsername(username: string): Promise<User | undefined>;
  createUser(user: InsertUser): Promise<User>;
  
  getProcessingJob(id: string): Promise<ProcessingJob | undefined>;
  getProcessingJobsByUser(userId?: string): Promise<ProcessingJob[]>;
  createProcessingJob(job: InsertProcessingJob): Promise<ProcessingJob>;
  updateProcessingJob(id: string, updates: Partial<ProcessingJob>): Promise<ProcessingJob | undefined>;
  deleteProcessingJob(id: string): Promise<boolean>;
}

export class MemStorage implements IStorage {
  private users: Map<string, User>;
  private processingJobs: Map<string, ProcessingJob>;

  constructor() {
    this.users = new Map();
    this.processingJobs = new Map();
  }

  async getUser(id: string): Promise<User | undefined> {
    return this.users.get(id);
  }

  async getUserByUsername(username: string): Promise<User | undefined> {
    return Array.from(this.users.values()).find(
      (user) => user.username === username,
    );
  }

  async createUser(insertUser: InsertUser): Promise<User> {
    const id = randomUUID();
    const user: User = { ...insertUser, id };
    this.users.set(id, user);
    return user;
  }

  async getProcessingJob(id: string): Promise<ProcessingJob | undefined> {
    return this.processingJobs.get(id);
  }

  async getProcessingJobsByUser(userId?: string): Promise<ProcessingJob[]> {
    return Array.from(this.processingJobs.values())
      .filter(job => !userId || job.userId === userId)
      .sort((a, b) => new Date(b.createdAt || 0).getTime() - new Date(a.createdAt || 0).getTime());
  }

  async createProcessingJob(insertJob: InsertProcessingJob): Promise<ProcessingJob> {
    const id = randomUUID();
    const job: ProcessingJob = {
      ...insertJob,
      id,
      status: "pending",
      progress: "0",
      createdAt: new Date(),
      userId: null,
      inputFileUrl: null,
      outputFileUrl: null,
      completedAt: null,
      metadata: insertJob.metadata || null,
    };
    this.processingJobs.set(id, job);
    return job;
  }

  async updateProcessingJob(id: string, updates: Partial<ProcessingJob>): Promise<ProcessingJob | undefined> {
    const job = this.processingJobs.get(id);
    if (!job) return undefined;
    
    const updatedJob = { ...job, ...updates };
    this.processingJobs.set(id, updatedJob);
    return updatedJob;
  }

  async deleteProcessingJob(id: string): Promise<boolean> {
    return this.processingJobs.delete(id);
  }
}

export const storage = new MemStorage();
