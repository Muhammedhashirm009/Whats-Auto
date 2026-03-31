document.addEventListener('DOMContentLoaded', () => {
    // ---- Global Elements ----
    const statusDot = document.getElementById('status-dot');
    const statusText = document.getElementById('status-text');
    
    // ---- Page Specific Elements ----
    
    // Dashboard
    const metricSent = document.getElementById('metric-sent');
    const metricScheduled = document.getElementById('metric-scheduled');
    const metricFailed = document.getElementById('metric-failed');

    // Single Send
    const sendMessageForm = document.getElementById('sendMessageForm');
    const singleFeedback = document.getElementById('singleFeedback');
    const sendBtn = document.getElementById('sendBtn');
    
    // Bulk Send
    const bulkForm = document.getElementById('bulkForm');
    const bulkFeedback = document.getElementById('bulkFeedback');
    const bulkBtn = document.getElementById('bulkBtn');

    // Schedule
    const scheduleForm = document.getElementById('scheduleForm');
    const schFeedback = document.getElementById('schFeedback');
    const schBtn = document.getElementById('schBtn');

    // Connection Management
    const loggedInView = document.getElementById('logged-in-view');
    const loggedOutView = document.getElementById('logged-out-view');
    const disconnectedView = document.getElementById('disconnected-view');
    const qrDisplay = document.getElementById('qr-display');
    const qrLoading = document.getElementById('qr-loading');
    const qrError = document.getElementById('qr-error');
    const qrImage = document.getElementById('qr-image');
    
    const disconnectBtn = document.getElementById('disconnectBtn');
    const logoutBtn = document.getElementById('logoutBtn');
    const connectBtn = document.getElementById('connectBtn');
    const retryQRBtn = document.getElementById('retryQRBtn');
    const startQRBtn = document.getElementById('startQRBtn');
    const connFeedback = document.getElementById('connFeedback');

    let isConnected = false;
    let isLoggedIn = false;
    let qrPollInterval = null;

    // ---- Status & Metrics Polling ----
    async function checkStatus() {
        try {
            const res = await fetch('/api/status');
            const data = await res.json();
            
            isConnected = data.connected;
            isLoggedIn = data.loggedIn;
            
            if (statusDot && statusText) {
                // Reset classes
                statusDot.className = 'w-2.5 h-2.5 rounded-full';
                
                if (isConnected && isLoggedIn) {
                    statusDot.classList.add('bg-emerald-500');
                    statusDot.classList.remove('animate-pulse');
                    statusText.textContent = 'Connected';
                    statusText.className = 'text-xs font-semibold text-emerald-700';
                } else if (isConnected && !isLoggedIn) {
                    statusDot.classList.add('bg-yellow-400', 'animate-pulse');
                    statusText.textContent = 'Awaiting Scan';
                    statusText.className = 'text-xs font-semibold text-yellow-700';
                } else if (!isConnected && isLoggedIn) {
                    statusDot.classList.add('bg-yellow-400', 'animate-pulse');
                    statusText.textContent = 'Disconnected';
                    statusText.className = 'text-xs font-semibold text-yellow-700';
                } else {
                    statusDot.classList.add('bg-red-500', 'animate-pulse');
                    statusText.textContent = 'Logged Out';
                    statusText.className = 'text-xs font-semibold text-red-600';
                }
            }

            updateConnectionView();
        } catch (error) {
            if (statusDot && statusText) {
                statusDot.className = 'w-2.5 h-2.5 rounded-full bg-red-500 animate-pulse';
                statusText.textContent = 'Offline';
                statusText.className = 'text-xs font-semibold text-red-600';
            }
            isConnected = false;
            isLoggedIn = false;
        }
    }

    function updateConnectionView() {
        if (!loggedInView) return; // Not on connection page

        if (isLoggedIn && isConnected) {
            loggedInView.classList.remove('hidden');
            loggedOutView.classList.add('hidden');
            disconnectedView.classList.add('hidden');
            stopQRPoll();
        } else if (isLoggedIn && !isConnected) {
            loggedInView.classList.add('hidden');
            loggedOutView.classList.add('hidden');
            disconnectedView.classList.remove('hidden');
            stopQRPoll();
        } else {
            loggedInView.classList.add('hidden');
            loggedOutView.classList.remove('hidden');
            disconnectedView.classList.add('hidden');
            startQRPoll();
        }
    }

    let qrRetryCount = 0;
    const QR_MAX_RETRIES = 3;

    async function fetchQR() {
        if (!loggedOutView || loggedOutView.classList.contains('hidden')) return;

        try {
            const res = await fetch('/api/qr');
            const data = await res.json();

            if (data.code) {
                // QR code received — render it
                qrRetryCount = 0;
                const qrUrl = `https://api.qrserver.com/v1/create-qr-code/?size=256x256&data=${encodeURIComponent(data.code)}`;
                
                // Validate image loads before showing
                const testImg = new Image();
                testImg.onload = () => {
                    qrImage.src = qrUrl;
                    qrDisplay.classList.remove('hidden');
                    qrLoading.classList.add('hidden');
                    qrError.classList.add('hidden');
                };
                testImg.onerror = () => {
                    // External QR API failed — show error with context
                    showQRError('QR service unavailable. Click retry or scan the QR from the server terminal.');
                };
                testImg.src = qrUrl;

            } else if (data.reason) {
                // Backend told us why there's no QR
                switch (data.reason) {
                    case 'logged_in':
                        // Status poll should catch this, but force a status check
                        checkStatus();
                        break;
                    case 'connecting':
                        showQRLoading('Connecting to WhatsApp servers...');
                        break;
                    case 'not_initialized':
                        showQRLoading('Initializing bot...');
                        break;
                    case 'waiting':
                        showQRLoading('Generating QR code...');
                        break;
                    case 'qr_expired':
                        stopQRPoll();
                        showQRExpired();
                        break;
                    default:
                        showQRLoading('Preparing...');
                }
            } else {
                // Unknown response — keep loading for a few tries, then show error
                qrRetryCount++;
                if (qrRetryCount >= QR_MAX_RETRIES) {
                    showQRError(data.error || 'Unable to generate QR code. Please check server logs.');
                }
            }
        } catch (e) {
            qrRetryCount++;
            console.error('Failed to fetch QR', e);
            if (qrRetryCount >= QR_MAX_RETRIES) {
                showQRError('Cannot reach server. Check your connection.');
            }
        }
    }

    function showQRLoading(text) {
        qrDisplay.classList.add('hidden');
        qrError.classList.add('hidden');
        qrLoading.classList.remove('hidden');
        const loadingText = qrLoading.querySelector('p');
        if (loadingText) loadingText.textContent = text || 'Generating QR Code...';
    }

    function showQRError(text) {
        qrDisplay.classList.add('hidden');
        qrLoading.classList.add('hidden');
        qrError.classList.remove('hidden');
        const errorText = qrError.querySelector('p');
        if (errorText) errorText.textContent = text || 'Failed to load QR code';
        // Hide start button, show retry
        if (startQRBtn) startQRBtn.classList.add('hidden');
        if (retryQRBtn) retryQRBtn.classList.remove('hidden');
    }

    function showQRExpired() {
        qrDisplay.classList.add('hidden');
        qrLoading.classList.add('hidden');
        qrError.classList.remove('hidden');
        const errorText = qrError.querySelector('p');
        if (errorText) errorText.textContent = 'QR code expired. Click below to generate new codes.';
        // Show start button, hide retry
        if (retryQRBtn) retryQRBtn.classList.add('hidden');
        if (startQRBtn) startQRBtn.classList.remove('hidden');
    }

    async function triggerStartQR() {
        showQRLoading('Starting QR scan...');
        if (startQRBtn) startQRBtn.classList.add('hidden');
        try {
            await fetch('/api/start-qr', { method: 'POST' });
        } catch(e) { /* ignore */ }
        // Wait a moment for backend to initialize, then start polling
        setTimeout(() => {
            qrRetryCount = 0;
            startQRPoll();
        }, 2000);
    }

    function startQRPoll() {
        if (qrPollInterval) return;
        qrRetryCount = 0;
        showQRLoading('Generating QR Code...');
        fetchQR();
        qrPollInterval = setInterval(fetchQR, 5000);
    }

    function stopQRPoll() {
        if (qrPollInterval) {
            clearInterval(qrPollInterval);
            qrPollInterval = null;
        }
    }

    async function fetchMetrics() {
        if (!metricSent) return;
        
        try {
            const res = await fetch('/api/metrics');
            const data = await res.json();
            metricSent.textContent = data.total_sent || '0';
            metricFailed.textContent = data.total_failed || '0';
            metricScheduled.textContent = data.scheduled_count || '0';
        } catch(e) {
            console.error('Failed to fetch metrics', e);
        }
    }

    // Initial check and regular polling
    checkStatus();
    fetchMetrics();
    setInterval(checkStatus, 3000);
    setInterval(fetchMetrics, 5000);

    // ---- Helper ----
    function showFeedback(element, message, type) {
        if (!element) return;
        element.textContent = message;
        element.classList.remove('hidden');
        
        // Tailwind-based feedback styling
        if (type === 'success') {
            element.className = 'mt-4 px-4 py-3 rounded-xl text-sm font-medium text-center bg-secondary-container/30 text-on-secondary-container border border-secondary/20';
        } else {
            element.className = 'mt-4 px-4 py-3 rounded-xl text-sm font-medium text-center bg-error-container text-on-error-container border border-error/20';
        }
        
        setTimeout(() => {
            element.classList.add('hidden');
        }, 5000);
    }

    // ---- Connection Handlers ----
    if (disconnectBtn) {
        disconnectBtn.addEventListener('click', async () => {
            try {
                const res = await fetch('/api/disconnect', { method: 'POST' });
                const data = await res.json();
                if (data.success) {
                    showFeedback(connFeedback, 'Disconnected successfully', 'success');
                    checkStatus();
                } else {
                    showFeedback(connFeedback, data.error || 'Failed to disconnect', 'error');
                }
            } catch (e) {
                showFeedback(connFeedback, 'Network error', 'error');
            }
        });
    }

    if (connectBtn) {
        connectBtn.addEventListener('click', async () => {
            try {
                const res = await fetch('/api/connect', { method: 'POST' });
                const data = await res.json();
                if (data.success) {
                    showFeedback(connFeedback, 'Connecting...', 'success');
                    checkStatus();
                } else {
                    showFeedback(connFeedback, data.error || 'Failed to connect', 'error');
                }
            } catch (e) {
                showFeedback(connFeedback, 'Network error', 'error');
            }
        });
    }

    if (logoutBtn) {
        logoutBtn.addEventListener('click', async () => {
            if (!confirm('Are you sure you want to logout? This will wipe your session data.')) return;
            try {
                const res = await fetch('/api/logout', { method: 'POST' });
                const data = await res.json();
                if (data.success) {
                    showFeedback(connFeedback, 'Logged out. Redirecting to scan...', 'success');
                    setTimeout(() => location.reload(), 2000);
                } else {
                    showFeedback(connFeedback, data.error || 'Failed to logout', 'error');
                }
            } catch (e) {
                showFeedback(connFeedback, 'Network error', 'error');
            }
        });
    }

    if (retryQRBtn) {
        retryQRBtn.addEventListener('click', () => {
            triggerStartQR();
        });
    }

    if (startQRBtn) {
        startQRBtn.addEventListener('click', () => {
            triggerStartQR();
        });
    }

    // ---- Single Send Handler ----
    if (sendMessageForm) {
        sendMessageForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            if (!isConnected || !isLoggedIn) return showFeedback(singleFeedback, 'Bot is not connected/logged in', 'error');

            const to = document.getElementById('phoneNumber').value.trim();
            const message = document.getElementById('messageBody').value.trim();
            const fileInput = document.getElementById('fileAttachment');
            const file = fileInput.files[0];

            if (!to || (!message && !file)) {
                return showFeedback(singleFeedback, 'Please provide a phone number and message/file', 'error');
            }

            const ogText = sendBtn.innerHTML;
            sendBtn.disabled = true;
            sendBtn.classList.add('opacity-60');
            sendBtn.querySelector('.btn-text').textContent = 'Sending...';

            try {
                let res;
                if (file) {
                    const formData = new FormData();
                    formData.append('to', to);
                    formData.append('message', message);
                    formData.append('file', file);
                    res = await fetch('/api/send', { method: 'POST', body: formData });
                } else {
                    res = await fetch('/api/send', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ to, message })
                    });
                }

                const data = await res.json();
                if (res.ok && data.success) {
                    showFeedback(singleFeedback, 'Message sent successfully!', 'success');
                    document.getElementById('messageBody').value = ''; 
                    fileInput.value = ''; 
                } else {
                    throw new Error(data.error || 'Failed to send');
                }
            } catch (error) {
                showFeedback(singleFeedback, error.message, 'error');
            } finally {
                sendBtn.disabled = false;
                sendBtn.classList.remove('opacity-60');
                sendBtn.innerHTML = ogText;
            }
        });
    }

    // ---- Bulk Send Handler ----
    if (bulkForm) {
        bulkForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            if (!isConnected || !isLoggedIn) return showFeedback(bulkFeedback, 'Bot is not connected/logged in', 'error');

            const delay = parseInt(document.getElementById('bulkDelay').value) || 2000;
            const csvData = document.getElementById('bulkData').value.trim();

            if (!csvData) return showFeedback(bulkFeedback, 'Please provide recipient data', 'error');

            const lines = csvData.split('\n');
            const messages = [];

            for (let i = 0; i < lines.length; i++) {
                const line = lines[i].trim();
                if(!line) continue;
                
                const commaIndex = line.indexOf(',');
                if(commaIndex === -1) {
                    return showFeedback(bulkFeedback, `Invalid format on line ${i+1}. Expected: Phone, Message`, 'error');
                }
                
                const to = line.substring(0, commaIndex).trim();
                const msg = line.substring(commaIndex + 1).trim();
                
                if(!to || !msg) {
                    return showFeedback(bulkFeedback, `Invalid data on line ${i+1}. Missing phone or message.`, 'error');
                }
                messages.push({ to, message: msg });
            }

            const ogText = bulkBtn.innerHTML;
            bulkBtn.disabled = true;
            bulkBtn.classList.add('opacity-60');
            bulkBtn.querySelector('.btn-text').textContent = 'Dispatching...';

            try {
                const res = await fetch('/api/bulk-send', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ messages, interval_ms: delay })
                });

                const data = await res.json();
                if (res.ok && data.success) {
                    showFeedback(bulkFeedback, `Started dispatching ${messages.length} messages in the background!`, 'success');
                    document.getElementById('bulkData').value = '';
                } else {
                    throw new Error(data.error || 'Failed to start bulk send');
                }
            } catch (error) {
                showFeedback(bulkFeedback, error.message, 'error');
            } finally {
                bulkBtn.disabled = false;
                bulkBtn.classList.remove('opacity-60');
                bulkBtn.innerHTML = ogText;
            }
        });
    }

    // ---- Schedule Handler ----
    if (scheduleForm) {
        scheduleForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            
            const to = document.getElementById('schPhoneNumber').value.trim();
            const timeVal = document.getElementById('schTime').value;
            const message = document.getElementById('schMessageBody').value.trim();

            if (!to || !timeVal || !message) {
                return showFeedback(schFeedback, 'Please fill out all fields', 'error');
            }

            const isoTime = new Date(timeVal).toISOString();

            const ogText = schBtn.innerHTML;
            schBtn.disabled = true;
            schBtn.classList.add('opacity-60');
            schBtn.querySelector('.btn-text').textContent = 'Scheduling...';

            try {
                const res = await fetch('/api/schedule', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ to, message, scheduled_for: isoTime })
                });

                const data = await res.json();
                if (res.ok && data.success) {
                    showFeedback(schFeedback, 'Message successfully scheduled!', 'success');
                    document.getElementById('schMessageBody').value = '';
                    document.getElementById('schTime').value = '';
                } else {
                    throw new Error(data.error || 'Failed to schedule');
                }
            } catch (error) {
                showFeedback(schFeedback, error.message, 'error');
            } finally {
                schBtn.disabled = false;
                schBtn.classList.remove('opacity-60');
                schBtn.innerHTML = ogText;
            }
        });
    }
});
