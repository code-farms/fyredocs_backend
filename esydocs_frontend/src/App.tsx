import { Toaster } from "@/components/ui/toaster";
import { Toaster as Sonner } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Routes, Route } from "react-router-dom";
import Index from "./pages/Index";
import MergePage from "./pages/MergePage";
import SplitPage from "./pages/SplitPage";
import CompressPage from "./pages/CompressPage";
import ConvertPage from "./pages/ConvertPage";
import PdfToWordPage from "./pages/PdfToWordPage";
import WordToPdfPage from "./pages/WordToPdfPage";
import PdfToExcelPage from "./pages/PdfToExcelPage";
import ExcelToPdfPage from "./pages/ExcelToPdfPage";
import PdfToImagePage from "./pages/PdfToImagePage";
import ImageToPdfPage from "./pages/ImageToPdfPage";
import NotFound from "./pages/NotFound";

const queryClient = new QueryClient();

const App = () => (
  <QueryClientProvider client={queryClient}>
    <TooltipProvider>
      <Toaster />
      <Sonner />
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<Index />} />
          <Route path="/merge" element={<MergePage />} />
          <Route path="/split" element={<SplitPage />} />
          <Route path="/compress" element={<CompressPage />} />
          <Route path="/convert" element={<ConvertPage />} />
          <Route path="/pdf-to-word" element={<PdfToWordPage />} />
          <Route path="/word-to-pdf" element={<WordToPdfPage />} />
          <Route path="/pdf-to-excel" element={<PdfToExcelPage />} />
          <Route path="/excel-to-pdf" element={<ExcelToPdfPage />} />
          <Route path="/pdf-to-image" element={<PdfToImagePage />} />
          <Route path="/image-to-pdf" element={<ImageToPdfPage />} />
          {/* ADD ALL CUSTOM ROUTES ABOVE THE CATCH-ALL "*" ROUTE */}
          <Route path="*" element={<NotFound />} />
        </Routes>
      </BrowserRouter>
    </TooltipProvider>
  </QueryClientProvider>
);

export default App;
