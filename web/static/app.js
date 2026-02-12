/**
 * EpisodeX - TV Series Audio Track Manager
 * Hash-based SPA with 4 pages: Series List, Series Detail, Season Detail, Updates
 */

const state = {
    series: [],
    updates: [],
    currentView: null,
    currentSeriesId: null,
    currentSeasonNum: null,
};

// Inline SVG placeholder for series without posters
const PLACEHOLDER_SVG = `data:image/svg+xml,${encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 450" fill="none"><rect width="300" height="450" fill="#1a1c22"/><rect x="1" y="1" width="298" height="448" stroke="#323640" stroke-width="2" fill="none"/><g transform="translate(150, 180)"><circle cx="0" cy="0" r="60" stroke="#3d4250" stroke-width="3" fill="none"/><circle cx="0" cy="0" r="20" stroke="#3d4250" stroke-width="3" fill="none"/><line x1="0" y1="-20" x2="0" y2="-60" stroke="#3d4250" stroke-width="3"/><line x1="0" y1="20" x2="0" y2="60" stroke="#3d4250" stroke-width="3"/><line x1="-20" y1="0" x2="-60" y2="0" stroke="#3d4250" stroke-width="3"/><line x1="20" y1="0" x2="60" y2="0" stroke="#3d4250" stroke-width="3"/><line x1="-14" y1="-14" x2="-42" y2="-42" stroke="#3d4250" stroke-width="3"/><line x1="14" y1="14" x2="42" y2="42" stroke="#3d4250" stroke-width="3"/><line x1="-14" y1="14" x2="-42" y2="42" stroke="#3d4250" stroke-width="3"/><line x1="14" y1="-14" x2="42" y2="-42" stroke="#3d4250" stroke-width="3"/><circle cx="0" cy="-40" r="8" fill="#282c37"/><circle cx="0" cy="40" r="8" fill="#282c37"/><circle cx="-40" cy="0" r="8" fill="#282c37"/><circle cx="40" cy="0" r="8" fill="#282c37"/><circle cx="-28" cy="-28" r="8" fill="#282c37"/><circle cx="28" cy="28" r="8" fill="#282c37"/><circle cx="-28" cy="28" r="8" fill="#282c37"/><circle cx="28" cy="-28" r="8" fill="#282c37"/></g><text x="150" y="320" font-family="system-ui, sans-serif" font-size="48" font-weight="bold" fill="#3d4250" text-anchor="middle">?</text><text x="150" y="380" font-family="system-ui, sans-serif" font-size="18" fill="#3d4250" text-anchor="middle">No Poster</text></svg>')}`;

function posterSrc(url) {
    return url || PLACEHOLDER_SVG;
}

// HTML escape utility to prevent XSS
function esc(str) {
    if (str == null) return '';
    const div = document.createElement('div');
    div.textContent = String(str);
    return div.innerHTML;
}

// ==============================================================================
// API
// ==============================================================================
const api = {
    async get(url) {
        const res = await fetch(url);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
    },
    async post(url, data) {
        const res = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data)
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
    },
    async put(url, data) {
        const res = await fetch(url, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data)
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
    },
    async delete(url) {
        const res = await fetch(url, { method: 'DELETE' });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
    }
};

// ==============================================================================
// Toast Notifications
// ==============================================================================
function showToast(message, type = 'success') {
    const container = document.getElementById('toast-container');
    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;
    toast.innerHTML = `<span class="toast-message">${esc(message)}</span>`;
    container.appendChild(toast);
    setTimeout(() => {
        toast.classList.add('hiding');
        setTimeout(() => toast.remove(), 300);
    }, 3000);
}

// ==============================================================================
// Routing
// ==============================================================================
function router() {
    const hash = window.location.hash.slice(1) || '/';

    if (hash === '/' || hash === '/series') {
        showSeriesListPage();
    } else if (hash.match(/^\/series\/(\d+)$/)) {
        const match = hash.match(/^\/series\/(\d+)$/);
        const seriesId = parseInt(match[1]);
        showSeriesDetailPage(seriesId);
    } else if (hash.match(/^\/series\/(\d+)\/season\/(\d+)$/)) {
        const match = hash.match(/^\/series\/(\d+)\/season\/(\d+)$/);
        const seriesId = parseInt(match[1]);
        const seasonNum = parseInt(match[2]);
        showSeasonDetailPage(seriesId, seasonNum);
    } else if (hash === '/updates') {
        showUpdatesPage();
    } else if (hash === '/add-series') {
        showAddSeriesPage();
    }
}

function navigate(path) {
    window.location.hash = path;
}

// ==============================================================================
// Page 1: Series List
// ==============================================================================
async function showSeriesListPage() {
    state.currentView = 'series';
    hideAllPages();
    document.getElementById('page-series').classList.add('active');
    updateNav('series');
    await loadSeries();
}

async function loadSeries() {
    const grid = document.getElementById('series-grid');
    const loading = document.getElementById('loading-state');
    const empty = document.getElementById('empty-state');

    loading.style.display = 'flex';
    grid.innerHTML = '';

    try {
        state.series = await api.get('/api/series');
        loading.style.display = 'none';

        if (state.series.length === 0) {
            empty.style.display = 'flex';
            return;
        }
        empty.style.display = 'none';
        renderSeries();
        updateStats();
    } catch (e) {
        loading.style.display = 'none';
        showToast('Failed to load series', 'error');
    }
}

function renderSeries() {
    const grid = document.getElementById('series-grid');
    const searchQuery = document.getElementById('search-input').value.toLowerCase();
    const filter = document.querySelector('.filter-btn.active')?.dataset.filter || 'all';
    const sort = document.getElementById('sort-select').value;

    let list = [...state.series];

    // Filter
    if (filter === 'continuing') list = list.filter(s => s.status?.toLowerCase() === 'continuing');
    else if (filter === 'ended') list = list.filter(s => s.status?.toLowerCase() === 'ended');

    // Search
    if (searchQuery) {
        list = list.filter(s =>
            s.title.toLowerCase().includes(searchQuery) ||
            (s.original_title && s.original_title.toLowerCase().includes(searchQuery))
        );
    }

    // Sort
    if (sort === 'title') list.sort((a, b) => a.title.localeCompare(b.title));
    else if (sort === 'title-desc') list.sort((a, b) => b.title.localeCompare(a.title));
    else list.sort((a, b) => new Date(b.created_at) - new Date(a.created_at));

    grid.innerHTML = list.map(s => {
        const hasNoMatch = !s.tvdb_id;
        return `
        <div class="series-card ${hasNoMatch ? 'unmatched' : ''}" onclick="${hasNoMatch ? '' : `navigate('/series/${s.id}')`}">
            <div class="series-poster">
                <img src="${posterSrc(s.poster_url)}" alt="${esc(s.title)}" loading="lazy">
                ${hasNoMatch ? '<div class="unmatched-overlay"></div>' : ''}
            </div>
            <div class="series-info">
                <h4 class="series-title">${esc(s.title)}</h4>
                <div class="series-meta">
                    <span>${s.watched_seasons || 0} seasons</span>
                    <span class="series-status-badge ${esc(s.status)}">${esc(s.status)}</span>
                </div>
                ${hasNoMatch ? `
                    <button class="btn btn-match" onclick="event.stopPropagation(); openMatchModal(${s.id})">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
                            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
                        </svg>
                        Match
                    </button>
                ` : ''}
            </div>
        </div>
        `;
    }).join('');
}

function updateStats() {
    document.getElementById('stat-total').textContent = state.series.length;
    document.getElementById('stat-continuing').textContent = state.series.filter(s => s.status?.toLowerCase() === 'continuing').length;
    document.getElementById('stat-ended').textContent = state.series.filter(s => s.status?.toLowerCase() === 'ended').length;
    document.getElementById('stat-seasons').textContent = state.series.reduce((sum, s) => sum + (s.watched_seasons || 0), 0);
}

// ==============================================================================
// Page 2: Series Detail
// ==============================================================================
async function showSeriesDetailPage(seriesId) {
    state.currentView = 'series-detail';
    state.currentSeriesId = seriesId;
    hideAllPages();
    document.getElementById('page-series-detail').classList.add('active');
    updateNav('series');
    await loadSeriesDetail(seriesId);
}

async function loadSeriesDetail(seriesId) {
    try {
        const series = await api.get(`/api/series/${seriesId}`);

        // Hero backdrop
        const backdropEl = document.getElementById('series-hero-backdrop');
        if (series.backdrop_url) {
            backdropEl.style.backgroundImage = `url(${series.backdrop_url})`;
        } else if (series.poster_url) {
            backdropEl.style.backgroundImage = `url(${series.poster_url})`;
        } else {
            backdropEl.style.backgroundImage = 'none';
        }

        // Poster & title
        document.getElementById('detail-series-title').textContent = series.title;
        document.getElementById('detail-original-title').textContent = series.original_title || '';
        document.getElementById('detail-series-poster').src = posterSrc(series.poster_url);

        // Metadata tags
        const tagsEl = document.getElementById('detail-tags');
        let tagsHTML = `<span id="detail-series-status" class="series-status-badge ${series.status || 'unknown'}">${series.status || 'unknown'}</span>`;
        if (series.year) tagsHTML += `<span class="meta-tag">${series.year}</span>`;
        if (series.content_rating) tagsHTML += `<span class="meta-tag">${series.content_rating}</span>`;
        if (series.runtime) tagsHTML += `<span class="meta-tag">${series.runtime} min</span>`;
        if (series.rating) tagsHTML += `<span class="meta-tag meta-tag-star">${series.rating.toFixed(1)}</span>`;
        if (series.genres && series.genres.length > 0) {
            series.genres.forEach(g => {
                const name = typeof g === 'string' ? g : (g.name || g);
                tagsHTML += `<span class="meta-tag">${name}</span>`;
            });
        }
        if (series.networks && series.networks.length > 0) {
            series.networks.forEach(n => {
                const name = typeof n === 'string' ? n : (n.name || n);
                tagsHTML += `<span class="meta-tag meta-tag-network">${name}</span>`;
            });
        }
        tagsEl.innerHTML = tagsHTML;

        // Overview
        const overviewEl = document.getElementById('detail-overview');
        overviewEl.textContent = series.overview || '';
        overviewEl.style.display = series.overview ? 'block' : 'none';

        // Sync button (show only if series has tvdb_id)
        const syncBtn = document.getElementById('sync-tvdb-btn');
        syncBtn.style.display = series.tvdb_id ? 'inline-flex' : 'none';

        // Characters
        const charsSection = document.getElementById('series-characters');
        const charsRow = document.getElementById('characters-row');
        if (series.characters && series.characters.length > 0) {
            charsSection.style.display = 'block';
            charsRow.innerHTML = series.characters.map(c => `
                <div class="character-card">
                    <div class="character-avatar">
                        <img src="${posterSrc(c.image_url)}" alt="${c.character_name || ''}" loading="lazy">
                    </div>
                    <div class="character-name">${c.character_name || ''}</div>
                    <div class="character-actor">${c.actor_name || ''}</div>
                </div>
            `).join('');
        } else {
            charsSection.style.display = 'none';
        }

        // Load seasons
        const seasons = await api.get(`/api/series/${seriesId}/seasons`);
        renderSeasons(series, seasons);

    } catch (e) {
        showToast('Failed to load series detail', 'error');
        console.error(e);
    }
}

function renderSeasons(series, seasons) {
    const grid = document.getElementById('seasons-grid');

    grid.innerHTML = seasons.map(season => {
        const owned = season.owned === true;
        const seasonImage = season.image || series.poster_url || PLACEHOLDER_SVG;

        if (!owned) {
            // Locked season
            return `
                <div class="season-card locked" data-season="${season.season_number}">
                    <div class="season-poster">
                        <img src="${seasonImage}" alt="Season ${season.season_number}" loading="lazy">
                        <div class="season-overlay">
                            <div class="season-lock-icon">
                                <svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                    <rect x="3" y="11" width="18" height="11" rx="2" ry="2"></rect>
                                    <path d="M7 11V7a5 5 0 0 1 10 0v4"></path>
                                </svg>
                            </div>
                            <div class="season-number">Season ${season.season_number}</div>
                            <span class="not-available-badge">Not Available</span>
                        </div>
                    </div>
                </div>
            `;
        } else {
            // Owned season - clickable card with optional voice badge
            const voiceBadge = season.voice_actor_name
                ? `<span class="voice-badge">${season.voice_actor_name}</span>`
                : '';
            return `
                <div class="season-card owned" data-season="${season.season_number}" onclick="navigate('/series/${series.id}/season/${season.season_number}')">
                    <div class="season-poster">
                        <img src="${seasonImage}" alt="Season ${season.season_number}" loading="lazy">
                        <div class="season-overlay">
                            <div class="season-number">Season ${season.season_number}</div>
                            ${voiceBadge}
                        </div>
                    </div>
                </div>
            `;
        }
    }).join('');
}


async function syncWithTVDB() {
    if (!state.currentSeriesId) return;
    const btn = document.getElementById('sync-tvdb-btn');
    btn.disabled = true;
    try {
        await api.post(`/api/series/${state.currentSeriesId}/sync`);
        showToast('Synced with TVDB');
        await loadSeriesDetail(state.currentSeriesId);
    } catch (e) {
        showToast('Sync failed', 'error');
    } finally {
        btn.disabled = false;
    }
}

async function deleteSeries() {
    if (!state.currentSeriesId) return;
    const series = state.series.find(s => s.id === state.currentSeriesId);
    if (!confirm(`Delete "${series?.title}"?`)) return;

    try {
        await api.delete(`/api/series/${state.currentSeriesId}`);
        showToast('Series deleted');
        navigate('/series');
    } catch (e) {
        showToast('Failed to delete series', 'error');
    }
}

// ==============================================================================
// Page 3: Season Detail (Audio Cutter)
// ==============================================================================
async function showSeasonDetailPage(seriesId, seasonNum) {
    state.currentView = 'season-detail';
    state.currentSeriesId = seriesId;
    state.currentSeasonNum = seasonNum;

    hideAllPages();
    const page = document.getElementById('page-season-detail');
    page.classList.add('active');

    // Reset page state
    document.getElementById('season-detail-title').textContent = `Season ${seasonNum}`;
    document.getElementById('season-detail-subtitle').textContent = '';
    document.getElementById('season-not-owned').style.display = 'none';
    document.getElementById('voice-selector-panel').style.display = 'flex';
    document.getElementById('audio-tracks-container').style.display = 'none';
    document.getElementById('season-files-box').style.display = 'block';
    document.getElementById('files-list').innerHTML = '<div class="loader"></div>';
    document.getElementById('audio-tracks-list').innerHTML = '';
    document.getElementById('progress-container').style.display = 'none';
    document.getElementById('processed-files-list').innerHTML = '';

    updateNav('series');

    // Load season data
    await loadSeasonDetail(seriesId, seasonNum);
}

let selectedTrackId = null;
let currentAudioPlayer = null;
let voiceSelectHandler = null;

async function loadSeasonDetail(seriesId, seasonNum) {
    try {
        // Load series info, voices list, and season info in parallel
        const [series, voices, seasonInfo] = await Promise.all([
            api.get(`/api/series/${seriesId}`),
            api.get('/api/voices'),
            api.get(`/api/series/${seriesId}/seasons/${seasonNum}`)
        ]);

        document.getElementById('season-detail-subtitle').textContent = series.title;

        // Populate voice selector
        const voiceSelect = document.getElementById('voice-select');
        if (voiceSelect) {
            voiceSelect.innerHTML = '<option value="">No voice selected</option>';
            voices.forEach(v => {
                const option = document.createElement('option');
                option.value = v.id;
                option.textContent = v.name;
                if (seasonInfo.voice_actor_id && seasonInfo.voice_actor_id === v.id) {
                    option.selected = true;
                }
                voiceSelect.appendChild(option);
            });
            // Remove old handler if exists, add new one
            if (voiceSelectHandler) {
                voiceSelect.removeEventListener('change', voiceSelectHandler);
            }
            voiceSelectHandler = () => {
                const voiceId = voiceSelect.value ? parseInt(voiceSelect.value) : null;
                updateSeasonVoice(seriesId, seasonNum, voiceId);
            };
            voiceSelect.addEventListener('change', voiceSelectHandler);
        }

        // Check if season is owned
        if (!seasonInfo.owned) {
            document.getElementById('season-not-owned').style.display = 'block';
            document.getElementById('voice-selector-panel').style.display = 'none';
            document.getElementById('season-files-box').style.display = 'none';
            return;
        }

        // Load audio tracks
        const audioData = await api.get(`/api/series/${seriesId}/seasons/${seasonNum}/audio`);

        // Display files
        const filesList = document.getElementById('files-list');
        if (audioData.files && audioData.files.length > 0) {
            filesList.innerHTML = audioData.files.map(file => `
                <div class="file-item ${file.processed ? 'processed' : ''}">
                    <span class="file-name">${file.name}</span>
                    <span class="file-status ${file.processed ? 'processed' : 'pending'}">
                        ${file.processed ? 'Processed' : 'Pending'}
                    </span>
                </div>
            `).join('');
        } else {
            filesList.innerHTML = '<p>No MKV files found</p>';
        }

        // Display audio tracks
        if (audioData.audio_tracks && audioData.audio_tracks.length > 0) {
            document.getElementById('audio-tracks-container').style.display = 'block';
            const tracksList = document.getElementById('audio-tracks-list');

            // Store file path for preview
            window.currentAudioFilePath = audioData.files[0]?.path;

            tracksList.innerHTML = audioData.audio_tracks.map((track, index) => `
                <div class="track-item ${index === 0 ? 'selected' : ''}" id="track-item-${index}">
                    <input type="radio" name="audioTrack" value="${track.id}" id="track-radio-${index}"
                        ${index === 0 ? 'checked' : ''} onchange="selectTrack(${track.id}, ${index})">
                    <div class="track-info" onclick="selectTrack(${track.id}, ${index}); document.getElementById('track-radio-${index}').checked = true;">
                        <div class="track-name">
                            ${track.name || `Track ${track.id}`}
                            ${track.default ? ' <span class="default-badge">default</span>' : ''}
                        </div>
                        <div class="track-details">
                            ID: ${track.id} | Lang: ${track.language} | Codec: ${track.codec} | Channels: ${track.channels || 'N/A'}
                        </div>
                    </div>
                    <div class="track-actions">
                        <button class="btn-listen" id="listen-btn-${index}" onclick="event.stopPropagation(); togglePreview(${index})">
                            Listen
                        </button>
                    </div>
                    <div class="audio-player-wrapper" id="player-${index}" style="display: none;"></div>
                </div>
            `).join('');

            // Select first track by default
            if (audioData.audio_tracks.length > 0) {
                selectedTrackId = audioData.audio_tracks[0].id;
            }
        }

    } catch (e) {
        showToast('Failed to load season detail', 'error');
        console.error(e);
    }
}

async function updateSeasonVoice(seriesId, seasonNum, voiceActorId) {
    try {
        await api.put(`/api/series/${seriesId}/seasons/${seasonNum}`, {
            voice_actor_id: voiceActorId
        });
        showToast('Voice studio updated');
    } catch (e) {
        showToast('Failed to update voice studio', 'error');
        console.error(e);
    }
}

function selectTrack(trackId, index) {
    selectedTrackId = trackId;
    document.querySelectorAll('.track-item').forEach((item, i) => {
        item.classList.toggle('selected', i === index);
    });
}

async function togglePreview(index) {
    const button = document.getElementById(`listen-btn-${index}`);
    const playerDiv = document.getElementById(`player-${index}`);

    // If already playing, stop and hide
    if (playerDiv.style.display !== 'none') {
        // Stop audio
        const audio = playerDiv.querySelector('audio');
        if (audio) {
            audio.pause();
            audio.currentTime = 0;
        }
        playerDiv.innerHTML = '';
        playerDiv.style.display = 'none';
        button.textContent = 'Listen';
        button.classList.remove('playing');
        currentAudioPlayer = null;
        return;
    }

    // Stop any other playing audio first
    document.querySelectorAll('.audio-player-wrapper').forEach((p, i) => {
        if (i !== index && p.style.display !== 'none') {
            const audio = p.querySelector('audio');
            if (audio) audio.pause();
            p.innerHTML = '';
            p.style.display = 'none';
            const btn = document.getElementById(`listen-btn-${i}`);
            if (btn) {
                btn.textContent = 'Listen';
                btn.classList.remove('playing');
            }
        }
    });

    // Show loading state
    button.disabled = true;
    button.textContent = 'Loading...';
    button.classList.add('loading');

    try {
        const filePath = window.currentAudioFilePath;
        if (!filePath) {
            throw new Error('No file available for preview');
        }

        const result = await api.post(`/api/series/${state.currentSeriesId}/seasons/${state.currentSeasonNum}/audio/preview`, {
            file_path: filePath,
            track_index: index
        });

        // Create audio player
        playerDiv.innerHTML = `
            <audio controls autoplay>
                <source src="${result.preview_url}" type="audio/mpeg">
                Your browser does not support audio playback.
            </audio>
        `;
        playerDiv.style.display = 'block';

        currentAudioPlayer = playerDiv.querySelector('audio');

        // Update button to "Stop" state
        button.textContent = 'Stop';
        button.classList.remove('loading');
        button.classList.add('playing');

        // When audio ends, reset button
        currentAudioPlayer.addEventListener('ended', () => {
            button.textContent = 'Listen';
            button.classList.remove('playing');
        });

        // When audio is paused manually via controls
        currentAudioPlayer.addEventListener('pause', () => {
            if (currentAudioPlayer.currentTime >= currentAudioPlayer.duration - 0.1) {
                // Audio ended naturally
                button.textContent = 'Listen';
                button.classList.remove('playing');
            }
        });

    } catch (e) {
        playerDiv.innerHTML = '<p class="preview-error">Failed to generate preview</p>';
        playerDiv.style.display = 'block';
        showToast('Failed to generate preview', 'error');
        button.textContent = 'Listen';
        button.classList.remove('loading');
    } finally {
        button.disabled = false;
    }
}

async function processSeasonAudio() {
    if (!selectedTrackId) {
        showToast('Please select an audio track', 'error');
        return;
    }

    const keepOriginal = document.getElementById('keep-original-checkbox').checked;

    if (!confirm('Process all files in this season? This will remove unwanted audio tracks.')) {
        return;
    }

    // Hide tracks container, show progress
    document.getElementById('audio-tracks-container').style.display = 'none';
    document.getElementById('progress-container').style.display = 'block';

    try {
        const response = await fetch(`/api/series/${state.currentSeriesId}/seasons/${state.currentSeasonNum}/audio/process`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                track_id: selectedTrackId,
                keep_original: keepOriginal
            })
        });

        if (!response.ok) {
            throw new Error('Failed to start processing');
        }

        // Read SSE stream
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        let stats = { success: 0, error: 0, skipped: 0 };

        while (true) {
            const { done, value } = await reader.read();
            if (done) break;

            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n\n');
            buffer = lines.pop(); // Keep incomplete line in buffer

            for (const line of lines) {
                if (line.startsWith('data: ')) {
                    const data = JSON.parse(line.slice(6));
                    handleProgressEvent(data, stats);
                }
            }
        }

        showToast('Processing completed!');
        setTimeout(() => navigate(`/series/${state.currentSeriesId}`), 2000);

    } catch (e) {
        document.getElementById('progress-container').style.display = 'none';
        document.getElementById('audio-tracks-container').style.display = 'block';
        showToast('Processing failed', 'error');
    }
}

function handleProgressEvent(data, stats) {
    const progressBar = document.getElementById('progress-bar');
    const progressPercent = document.getElementById('progress-percent');
    const currentFileInfo = document.getElementById('current-file-info');
    const filesList = document.getElementById('processed-files-list');

    switch (data.type) {
        case 'start':
            document.getElementById('progress-text').textContent = `Processing ${data.total} files...`;
            break;

        case 'progress':
            const percent = Math.round((data.current / data.total) * 100);
            progressBar.style.width = percent + '%';
            progressPercent.textContent = percent + '%';
            currentFileInfo.innerHTML = `
                <div class="file-label">Processing (${data.current}/${data.total}):</div>
                <div class="file-name">${data.file}</div>
            `;
            break;

        case 'file_done':
            // Update stats
            if (data.status === 'success') stats.success++;
            else if (data.status === 'error') stats.error++;
            else if (data.status === 'skipped') stats.skipped++;

            document.getElementById('stat-success').textContent = stats.success;
            document.getElementById('stat-error').textContent = stats.error;
            document.getElementById('stat-skipped').textContent = stats.skipped;

            // Add to files list
            const statusClass = data.status;
            const statusText = data.status === 'success' ? 'Success' :
                              data.status === 'skipped' ? 'Skipped' : 'Error';

            filesList.innerHTML += `
                <div class="file-item">
                    <span class="file-name">${data.file}</span>
                    <span class="file-status ${statusClass}">${statusText}</span>
                </div>
            `;

            const percentDone = Math.round((data.current / data.total) * 100);
            progressBar.style.width = percentDone + '%';
            progressPercent.textContent = percentDone + '%';
            break;

        case 'complete':
            progressBar.style.width = '100%';
            progressPercent.textContent = '100%';
            document.getElementById('progress-text').textContent = 'Complete!';
            currentFileInfo.innerHTML = `
                <div class="file-label">Processing complete</div>
                <div class="file-name">Success: ${stats.success}, Errors: ${stats.error}, Skipped: ${stats.skipped}</div>
            `;
            break;
    }
}

// ==============================================================================
// Updates Page
// ==============================================================================
async function showUpdatesPage() {
    state.currentView = 'updates';
    hideAllPages();
    document.getElementById('page-updates').classList.add('active');
    updateNav('updates');
    await loadUpdates();
}

async function loadUpdates() {
    const list = document.getElementById('updates-list');
    const empty = document.getElementById('updates-empty');

    try {
        state.updates = await api.get('/api/updates');

        if (state.updates.length === 0) {
            list.innerHTML = '';
            empty.style.display = 'flex';
            updateBadge(0);
            return;
        }

        empty.style.display = 'none';
        updateBadge(state.updates.length);

        list.innerHTML = state.updates.map(u => {
            const total = u.total_seasons || 0;
            const watched = u.watched_seasons || 0;
            // Build a list of which season numbers are new (not owned)
            // We know total and watched count; show the range
            const newCount = u.new_seasons || (total - watched);
            let seasonDetail = '';
            if (newCount === 1) {
                seasonDetail = `Season ${total} available`;
            } else if (newCount > 0) {
                // Show range: e.g. "Seasons 3-5 available" (last N seasons are new)
                const from = watched + 1;
                const to = total;
                seasonDetail = `Seasons ${from}\u2013${to} available`;
            }
            return `
            <div class="update-card" onclick="navigate('/series/${u.id}')">
                <div class="update-poster">
                    <img src="${posterSrc(u.poster_url)}" alt="${u.title}">
                </div>
                <div class="update-info">
                    <h4 class="update-title">${u.title}</h4>
                    <p class="update-detail"><span class="update-season">${newCount} new season${newCount !== 1 ? 's' : ''}</span> \u2014 ${seasonDetail}</p>
                </div>
            </div>
            `;
        }).join('');
    } catch (e) {
        showToast('Failed to load updates', 'error');
    }
}

function updateBadge(count) {
    const badge = document.getElementById('updates-badge');
    if (count > 0) {
        badge.textContent = count;
        badge.style.display = 'flex';
    } else {
        badge.style.display = 'none';
    }
}

async function checkUpdates() {
    const btn = document.getElementById('check-updates-btn');
    btn.disabled = true;
    try {
        await api.post('/api/updates/check');
        await loadUpdates();
        showToast('Update check complete');
    } catch (e) {
        showToast('Update check failed', 'error');
    } finally {
        btn.disabled = false;
    }
}

// ==============================================================================
// Add Series Page
// ==============================================================================
async function showAddSeriesPage() {
    state.currentView = 'add-series';
    hideAllPages();
    document.getElementById('page-add-series').classList.add('active');
    updateNav('series');

    document.getElementById('tvdb-search-input').value = '';
    document.getElementById('search-results').innerHTML = '';
    document.getElementById('tvdb-search-input').focus();
}

let searchTimer;
async function searchTVDB(query) {
    if (query.length < 2) {
        document.getElementById('search-results').innerHTML = '';
        return;
    }

    try {
        const results = await api.get(`/api/search?q=${encodeURIComponent(query)}`);
        const container = document.getElementById('search-results');

        if (results.length === 0) {
            container.innerHTML = '<p class="no-results text-muted">No results found</p>';
            return;
        }

        container.innerHTML = results.map(r => `
            <div class="search-result" onclick="addSeries(${r.id})">
                <div class="search-result-poster">
                    <img src="${posterSrc(r.poster)}" alt="${esc(r.name)}">
                </div>
                <div class="search-result-info">
                    <div class="search-result-title">${esc(r.name)}</div>
                    <div class="search-result-year">${esc(r.year) || 'N/A'}</div>
                </div>
                <button class="btn btn-primary" onclick="event.stopPropagation(); addSeries(${r.id})">Add</button>
            </div>
        `).join('');
    } catch (e) {
        showToast('Search failed', 'error');
    }
}

async function addSeries(tvdbId) {
    try {
        await api.post('/api/series', { tvdb_id: tvdbId });
        navigate('/series');
        showToast('Series added');
    } catch (e) {
        showToast('Failed to add series', 'error');
    }
}

// ==============================================================================
// Alerts
// ==============================================================================
async function loadAlerts() {
    try {
        const alerts = await api.get('/api/alerts');
        const container = document.getElementById('alerts-container');
        if (!Array.isArray(alerts) || alerts.length === 0) {
            container.innerHTML = '';
            return;
        }
        container.innerHTML = alerts.map(alert => `
            <div class="alert alert-${alert.type.includes('error') ? 'error' : 'warning'}">
                <span class="alert-message">${esc(alert.message)}</span>
                <button class="alert-dismiss" onclick="dismissAlert(${alert.id})">x</button>
            </div>
        `).join('');
    } catch (e) {
        console.error('Failed to load alerts:', e);
    }
}

async function dismissAlert(id) {
    try {
        await api.post(`/api/alerts/${id}/dismiss`);
        loadAlerts();
    } catch (e) {
        showToast('Failed to dismiss alert', 'error');
    }
}

// ==============================================================================
// Scan
// ==============================================================================
async function triggerScan() {
    const btn = document.getElementById('scan-trigger');
    btn.classList.add('spinning');
    try {
        await api.post('/api/scan/trigger');
        showToast('Scan started');
        setTimeout(() => {
            if (state.currentView === 'series') {
                loadSeries();
            }
        }, 3000);
    } catch (e) {
        showToast('Scan failed', 'error');
    } finally {
        btn.classList.remove('spinning');
    }
}

// ==============================================================================
// Utility
// ==============================================================================
function hideAllPages() {
    document.querySelectorAll('.page').forEach(p => p.classList.remove('active'));
}

function updateNav(activePage) {
    document.querySelectorAll('.nav-link').forEach(link => {
        link.classList.toggle('active', link.dataset.page === activePage);
    });
}

// ==============================================================================
// Match Modal
// ==============================================================================
let matchSeriesId = null;
let matchSearchTimer = null;

function openMatchModal(seriesId) {
    matchSeriesId = seriesId;
    const modal = document.getElementById('match-modal');
    const input = document.getElementById('match-search-input');
    const results = document.getElementById('match-results');

    modal.classList.add('active');
    input.value = '';
    results.innerHTML = '';

    // Focus on search input
    setTimeout(() => input.focus(), 100);
}

function closeMatchModal() {
    const modal = document.getElementById('match-modal');
    modal.classList.remove('active');
    matchSeriesId = null;

    // Clear search
    document.getElementById('match-search-input').value = '';
    document.getElementById('match-results').innerHTML = '';
}

async function searchTVDBForMatch(query) {
    if (query.length < 2) {
        document.getElementById('match-results').innerHTML = '';
        return;
    }

    try {
        const results = await api.get(`/api/search?q=${encodeURIComponent(query)}`);
        const container = document.getElementById('match-results');

        if (results.length === 0) {
            container.innerHTML = '<p class="no-results text-muted">No results found</p>';
            return;
        }

        container.innerHTML = results.map(r => `
            <div class="match-result-item">
                <div class="match-result-poster">
                    <img src="${posterSrc(r.poster)}" alt="${esc(r.name)}">
                </div>
                <div class="match-result-info">
                    <div class="match-result-title">${esc(r.name)}</div>
                    <div class="match-result-meta">
                        <span class="match-result-year">${esc(r.year) || 'N/A'}</span>
                        ${r.status ? `<span class="match-result-status">${esc(r.status)}</span>` : ''}
                    </div>
                    ${r.status ? `<div class="match-result-overview">${esc(r.status)}</div>` : ''}
                </div>
                <button class="btn btn-primary" onclick="matchSeries(${r.id})">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
                        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
                    </svg>
                    Link
                </button>
            </div>
        `).join('');
    } catch (e) {
        showToast('Search failed', 'error');
        console.error(e);
    }
}

async function matchSeries(tvdbId) {
    if (!matchSeriesId) {
        showToast('No series selected', 'error');
        return;
    }

    try {
        const result = await api.post(`/api/series/${matchSeriesId}/match`, { tvdb_id: tvdbId });

        closeMatchModal();

        // Check if this was a merge operation
        if (result.merged) {
            showToast(result.message || 'Seasons merged successfully');
        } else {
            showToast('Series matched successfully');
        }

        // Reload series list to show updated data
        await loadSeries();
    } catch (e) {
        showToast('Failed to match series', 'error');
        console.error(e);
    }
}

// ==============================================================================
// Initialize
// ==============================================================================
document.addEventListener('DOMContentLoaded', () => {
    // Setup event listeners
    document.getElementById('search-input')?.addEventListener('input', renderSeries);
    document.getElementById('sort-select')?.addEventListener('change', renderSeries);

    document.querySelectorAll('.filter-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            document.querySelectorAll('.filter-btn').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            renderSeries();
        });
    });

    document.getElementById('tvdb-search-input')?.addEventListener('input', e => {
        clearTimeout(searchTimer);
        searchTimer = setTimeout(() => searchTVDB(e.target.value), 300);
    });

    document.getElementById('match-search-input')?.addEventListener('input', e => {
        clearTimeout(matchSearchTimer);
        matchSearchTimer = setTimeout(() => searchTVDBForMatch(e.target.value), 300);
    });

    document.getElementById('add-series-btn')?.addEventListener('click', () => navigate('/add-series'));
    document.getElementById('empty-add-btn')?.addEventListener('click', () => navigate('/add-series'));
    document.getElementById('scan-trigger')?.addEventListener('click', triggerScan);
    document.getElementById('check-updates-btn')?.addEventListener('click', checkUpdates);
    document.getElementById('delete-series-btn')?.addEventListener('click', deleteSeries);
    document.getElementById('sync-tvdb-btn')?.addEventListener('click', syncWithTVDB);

    // Back buttons
    document.getElementById('back-to-series')?.addEventListener('click', () => navigate('/series'));
    document.getElementById('back-to-series-from-season')?.addEventListener('click', () => {
        if (state.currentSeriesId) {
            navigate(`/series/${state.currentSeriesId}`);
        } else {
            navigate('/series');
        }
    });
    document.getElementById('back-from-add')?.addEventListener('click', () => navigate('/series'));

    // Process audio button
    document.getElementById('process-audio-btn')?.addEventListener('click', processSeasonAudio);

    // Setup router
    window.addEventListener('hashchange', router);
    router();

    // Load alerts periodically
    loadAlerts();
    setInterval(loadAlerts, 30000);
});

// Make functions globally accessible
window.navigate = navigate;
window.selectTrack = selectTrack;
window.togglePreview = togglePreview;
window.processSeasonAudio = processSeasonAudio;
window.addSeries = addSeries;
window.dismissAlert = dismissAlert;
window.deleteSeries = deleteSeries;
window.openMatchModal = openMatchModal;
window.closeMatchModal = closeMatchModal;
window.matchSeries = matchSeries;
window.syncWithTVDB = syncWithTVDB;
window.updateSeasonVoice = updateSeasonVoice;
