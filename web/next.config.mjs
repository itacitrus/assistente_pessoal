/** @type {import('next').NextConfig} */
const nextConfig = {
  // standalone gera um servidor Node minimo (.next/standalone) com só as deps
  // usadas — ideal pra empacotar em container e rodar `node server.js`.
  // Self-host no EC2 (Zello), não Vercel.
  output: "standalone",
};

export default nextConfig;
