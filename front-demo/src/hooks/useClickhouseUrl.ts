import { useState, useEffect } from 'react';

const STORAGE_KEY = 'clickhouse-server-url-v2';
const DEFAULT_URL = 'http://localhost:8123/';

export function useClickhouseUrl() {
  const [url, setUrl] = useState<string>(() => {
    const saved = localStorage.getItem(STORAGE_KEY);
    return saved || DEFAULT_URL;
  });

  useEffect(() => {
    localStorage.setItem(STORAGE_KEY, url);
  }, [url]);

  return { url, setUrl };
}

