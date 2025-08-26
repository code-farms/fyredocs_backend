# Overview

DocuFlow is a document processing web application that provides various tools for converting, organizing, and securing PDF and office documents. The application features a modern React frontend built with TypeScript and Vite, paired with an Express.js backend. Users can upload files, process them with different tools (PDF to Word, PDF to Excel, merge PDFs, etc.), and track processing jobs through a real-time queue system.

# User Preferences

Preferred communication style: Simple, everyday language.

# System Architecture

## Frontend Architecture
The client is built as a Single Page Application (SPA) using React 18 with TypeScript. The architecture follows modern React patterns:

- **Component Structure**: Uses shadcn/ui components for consistent UI design with Radix UI primitives
- **Routing**: Wouter for lightweight client-side routing with dynamic tool pages
- **State Management**: TanStack Query for server state management and caching
- **Styling**: Tailwind CSS with CSS custom properties for theming and responsive design
- **Build Tool**: Vite for fast development and optimized production builds

The app supports multiple document processing tools organized into categories (convert, organize, security), each with dedicated tool pages and file upload capabilities.

## Backend Architecture
The server uses Express.js with TypeScript in ESM format:

- **API Layer**: RESTful endpoints for job management (`GET /api/jobs`, `POST /api/jobs`, etc.)
- **File Processing**: Multer middleware for handling file uploads with 50MB size limits
- **Storage Abstraction**: Interface-based storage system with in-memory implementation (MemStorage) that can be extended for database persistence
- **Error Handling**: Centralized error middleware with structured error responses
- **Development Setup**: Custom Vite integration for SSR and HMR in development mode

## Data Storage Solutions
Currently uses an in-memory storage system with interfaces designed for easy migration to persistent storage:

- **Database Schema**: Drizzle ORM configured for PostgreSQL with user and processing job tables
- **Database Provider**: Neon Database serverless PostgreSQL (configured but not actively used)
- **Schema Design**: Users table for authentication and processing_jobs table for tracking file processing status, progress, and metadata

The schema supports job statuses (pending, processing, completed, failed), file metadata storage, and user association for multi-tenant support.

## Authentication and Authorization
Basic user management structure in place:

- **User Schema**: Username/password authentication model
- **Session Management**: Express sessions with PostgreSQL session store (connect-pg-simple)
- **Current State**: Authentication endpoints and middleware not fully implemented, currently uses simplified user association

## External Dependencies

### UI and Styling
- **shadcn/ui**: Complete component library built on Radix UI primitives
- **Tailwind CSS**: Utility-first CSS framework with custom design system
- **Lucide React**: Icon library for consistent iconography
- **React Hook Form**: Form validation and management

### File Processing
- **Multer**: Express middleware for handling multipart/form-data file uploads
- **File System**: Node.js fs module for temporary file storage during processing

### Database and ORM
- **Drizzle ORM**: Type-safe SQL query builder with PostgreSQL dialect
- **Neon Database**: Serverless PostgreSQL database service
- **Drizzle Kit**: Database migrations and schema management tool

### Development and Build Tools
- **Vite**: Frontend build tool with React plugin and custom runtime error overlay
- **TypeScript**: Static type checking across the entire codebase
- **ESBuild**: Server-side bundling for production deployment

### State Management and HTTP
- **TanStack Query**: Server state management, caching, and synchronization
- **Wouter**: Minimal React router for client-side navigation

The application is designed with a clear separation of concerns, making it easy to extend with new document processing tools and scale the backend infrastructure as needed.