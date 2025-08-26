import { sql } from "drizzle-orm";
import { pgTable, text, varchar, timestamp, jsonb } from "drizzle-orm/pg-core";
import { createInsertSchema } from "drizzle-zod";
import { z } from "zod";

export const users = pgTable("users", {
  id: varchar("id").primaryKey().default(sql`gen_random_uuid()`),
  username: text("username").notNull().unique(),
  password: text("password").notNull(),
});

export const processingJobs = pgTable("processing_jobs", {
  id: varchar("id").primaryKey().default(sql`gen_random_uuid()`),
  userId: varchar("user_id"),
  fileName: text("file_name").notNull(),
  fileSize: text("file_size").notNull(),
  toolType: text("tool_type").notNull(), // 'pdf-to-word', 'merge-pdf', etc.
  status: text("status").notNull().default("pending"), // 'pending', 'processing', 'completed', 'failed'
  progress: text("progress").default("0"),
  inputFileUrl: text("input_file_url"),
  outputFileUrl: text("output_file_url"),
  metadata: jsonb("metadata"), // Additional options/settings
  createdAt: timestamp("created_at").default(sql`NOW()`),
  completedAt: timestamp("completed_at"),
});

export const insertUserSchema = createInsertSchema(users).pick({
  username: true,
  password: true,
});

export const insertProcessingJobSchema = createInsertSchema(processingJobs).pick({
  fileName: true,
  fileSize: true,
  toolType: true,
  metadata: true,
});

export type InsertUser = z.infer<typeof insertUserSchema>;
export type User = typeof users.$inferSelect;
export type InsertProcessingJob = z.infer<typeof insertProcessingJobSchema>;
export type ProcessingJob = typeof processingJobs.$inferSelect;
