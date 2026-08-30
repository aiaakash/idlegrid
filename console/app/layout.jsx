import "./globals.css";

export const metadata = {
  title: "idlegrid console",
  description: "private inference on idle Macs",
};

export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
